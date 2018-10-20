package gochannel

import (
	"github.com/roblaszczak/gooddd/message"
	"sync"
	"time"
)

type subscriber struct {
	outputChannel chan message.Message
}

type GoChannel struct {
	subscribers     []*subscriber
	subscribersLock *sync.RWMutex
}

func (g GoChannel) Save(messages []message.Message) error {
	g.subscribersLock.RLock()
	defer g.subscribersLock.RUnlock()

	for _, message := range messages {
		for _, s := range g.subscribers {
			select {
			case s.outputChannel <- message:
				// todo - log it
				//sendLogger.Debug("sent messages to subscriber")
			case <-time.After(time.Second): // todo - config
				// todo - log it
				//sendLogger.Warn("cannot send messages")
			}
		}
	}

	return nil
}

// todo - topics support
func (g GoChannel) Subscribe(topic string) (chan message.Message, error) {
	g.subscribersLock.Lock()
	defer g.subscribersLock.Unlock()

	s := &subscriber{}
	g.subscribers = append(g.subscribers, s)

	return s.outputChannel, nil
}

func (g GoChannel) Close() error {
	// todo
	return nil
}
