package message

import (
	"fmt"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	sync_internal "github.com/ThreeDotsLabs/watermill/internal/sync"
	"github.com/pkg/errors"
)

type HandlerFunc func(msg *Message) ([]*Message, error)

type HandlerMiddleware func(h HandlerFunc) HandlerFunc

type RouterPlugin func(*Router) error

type RouterConfig struct {
	ServerName string

	CloseTimeout time.Duration
}

func (c *RouterConfig) setDefaults() {
	if c.CloseTimeout == 0 {
		c.CloseTimeout = time.Second * 30
	}
}

func (c RouterConfig) Validate() error {
	if c.ServerName == "" {
		return errors.New("empty ServerName")
	}

	return nil
}

func NewRouter(config RouterConfig, logger watermill.LoggerAdapter) (*Router, error) {
	config.setDefaults()
	if err := config.Validate(); err != nil {
		return nil, errors.Wrap(err, "invalid config")
	}

	return &Router{
		config: config,

		handlers: map[string]*handler{},

		handlersWg:        &sync.WaitGroup{},
		runningHandlersWg: &sync.WaitGroup{},

		closeCh:  make(chan struct{}),
		closedCh: make(chan struct{}),

		logger: logger,

		running: make(chan struct{}),
	}, nil
}

type Router struct {
	config RouterConfig

	middlewares []HandlerMiddleware

	plugins []RouterPlugin

	handlers map[string]*handler

	handlersWg        *sync.WaitGroup
	runningHandlersWg *sync.WaitGroup

	closeCh  chan struct{}
	closedCh chan struct{}
	closed   bool

	logger watermill.LoggerAdapter

	isRunning bool
	running   chan struct{}
}

func (r *Router) Logger() watermill.LoggerAdapter {
	return r.logger
}

// AddMiddleware adds a new middleware to the router.
//
// The order of middlewares matters. Middleware added at the beginning is executed first.
func (r *Router) AddMiddleware(m ...HandlerMiddleware) {
	r.logger.Debug("Adding middlewares", watermill.LogFields{"count": fmt.Sprintf("%d", len(m))})

	r.middlewares = append(r.middlewares, m...)
}

func (r *Router) AddPlugin(p ...RouterPlugin) {
	r.logger.Debug("Adding plugins", watermill.LogFields{"count": fmt.Sprintf("%d", len(p))})

	r.plugins = append(r.plugins, p...)
}

func (r *Router) AddHandler(
	handlerName string,
	subscribeTopic string,
	publishTopic string,
	pubSub PubSub,
	handlerFunc HandlerFunc,
) error {
	if err := r.AddNoPublisherHandler(handlerName, subscribeTopic, pubSub, handlerFunc); err != nil {
		return err
	}
	r.handlers[handlerName].publisher = pubSub
	r.handlers[handlerName].publishTopic = publishTopic

	return nil
}

func (r *Router) AddNoPublisherHandler(
	handlerName string,
	subscribeTopic string,
	subscriber Subscriber,
	handlerFunc HandlerFunc,
) error {
	r.logger.Info("Adding subscriber", watermill.LogFields{
		"handler_name": handlerName,
		"topic":        subscribeTopic,
	})

	if _, ok := r.handlers[handlerName]; ok {
		return errors.Errorf("handler %s already exists", handlerName)
	}

	r.handlers[handlerName] = &handler{
		name:              handlerName,
		subscribeTopic:    subscribeTopic,
		handlerFunc:       handlerFunc,
		subscriber:        subscriber,
		logger:            r.logger,
		runningHandlersWg: r.runningHandlersWg,
		closeCh:           r.closeCh,
	}

	return nil
}

func (r *Router) Run() (err error) {
	if r.isRunning {
		return errors.New("router is already running")
	}
	r.isRunning = true

	defer func() {
		if r := recover(); r != nil {
			err = errors.Errorf("panic recovered: %#v", r)
			return
		}
	}()

	r.logger.Debug("Loading plugins", nil)
	for _, plugin := range r.plugins {
		if err := plugin(r); err != nil {
			return errors.Wrapf(err, "cannot initialize plugin %v", plugin)
		}
	}

	for _, h := range r.handlers {
		r.logger.Debug("Subscribing to topic", watermill.LogFields{
			"subscriber_name": h.name,
			"topic":           h.subscribeTopic,
		})

		messages, err := h.subscriber.Subscribe(h.subscribeTopic)
		if err != nil {
			return errors.Wrapf(err, "cannot subscribe topic %s", h.subscribeTopic)
		}

		h.messagesCh = messages
	}

	for i := range r.handlers {
		handler := r.handlers[i]

		r.handlersWg.Add(1)

		go func() {
			handler.run(r.middlewares)

			r.handlersWg.Done()
			r.logger.Info("Subscriber stopped", watermill.LogFields{
				"subscriber_name": handler.name,
				"topic":           handler.subscribeTopic,
			})
		}()
	}

	close(r.running)

	<-r.closeCh

	r.logger.Info("Waiting for messages", watermill.LogFields{
		"timeout": r.config.CloseTimeout,
	})

	<-r.closedCh

	r.logger.Info("All messages processed", nil)

	return nil
}

// Running is closed when router is running.
// In other words: you can wait till router is running using
//     <- r.Running()
func (r *Router) Running() chan struct{} {
	return r.running
}

func (r *Router) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true

	r.logger.Info("Closing router", nil)
	defer r.logger.Info("Router closed", nil)

	close(r.closeCh)

	timeouted := sync_internal.WaitGroupTimeout(r.handlersWg, r.config.CloseTimeout)
	if timeouted {
		return errors.New("router close timeouted")
	}

	close(r.closedCh)

	return nil
}

type handler struct {
	name           string
	subscribeTopic string
	publishTopic   string
	handlerFunc    HandlerFunc

	publisher         Publisher
	subscriber        Subscriber
	logger            watermill.LoggerAdapter
	runningHandlersWg *sync.WaitGroup

	messagesCh chan *Message

	closeCh chan struct{}
}

func (h *handler) run(middlewares []HandlerMiddleware) {
	h.logger.Info("Starting handler", watermill.LogFields{
		"subscriber_name": h.name,
		"topic":           h.subscribeTopic,
	})

	middlewareHandler := h.handlerFunc

	// first added middlewares should be executed first (so should be at the top of call stack)
	for i := len(middlewares) - 1; i >= 0; i-- {
		middlewareHandler = middlewares[i](middlewareHandler)
	}

	go h.handleClose()

	for msg := range h.messagesCh {
		h.runningHandlersWg.Add(1)
		go h.handleMessage(msg, middlewareHandler)
	}

	if h.publisher != nil {
		h.logger.Debug("Waiting for publisher to close", nil)
		if err := h.publisher.Close(); err != nil {
			h.logger.Error("Failed to close publisher", err, nil)
		}
		h.logger.Debug("Publisher closed", nil)
	}

	h.logger.Debug("Router handler stopped", nil)

}

func (h *handler) handleClose() {
	<-h.closeCh

	h.logger.Debug("Waiting for subscriber to close", nil)

	if err := h.subscriber.Close(); err != nil {
		h.logger.Error("Failed to close subscriber", err, nil)
	}

	h.logger.Debug("Subscriber closed", nil)
}

func (h *handler) handleMessage(msg *Message, handler HandlerFunc) {
	defer h.runningHandlersWg.Done()

	defer func() {
		if recovered := recover(); recovered != nil {
			h.logger.Error("Panic recovered in handler", errors.Errorf("%s", recovered), nil)
			msg.Nack()
			return
		}

		msg.Ack()
	}()

	msgFields := watermill.LogFields{"message_uuid": msg.UUID}
	h.logger.Trace("Received message", msgFields)

	producedMessages, err := handler(msg)
	if err != nil {
		h.logger.Error("Handler returned error", err, nil)
		msg.Nack()
		return
	}

	if err := h.publishProducedMessages(producedMessages, msgFields); err != nil {
		h.logger.Error("Publishing produced messages failed", err, nil)
		msg.Nack()
		return
	}

	h.logger.Trace("Message processed", msgFields)
}

func (h *handler) publishProducedMessages(producedMessages Messages, msgFields watermill.LogFields) error {
	if len(producedMessages) == 0 {
		return nil
	}

	if h.publishTopic == "" {
		return errors.New("router was created without publisher, cannot publish messages")
	}

	h.logger.Trace("Sending produced messages", msgFields.Add(watermill.LogFields{
		"produced_messages_count": len(producedMessages),
	}))

	for _, msg := range producedMessages {
		if err := h.publisher.Publish(h.publishTopic, msg); err != nil {
			// todo - how to deal with it better/transactional/retry?
			h.logger.Error("Cannot publish message", err, msgFields.Add(watermill.LogFields{
				"not_sent_message": fmt.Sprintf("%#v", producedMessages),
			}))

			return err
		}
	}

	return nil
}
