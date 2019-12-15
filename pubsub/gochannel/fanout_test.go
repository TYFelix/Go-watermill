package gochannel_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
)

func TestFanOut(t *testing.T) {
	logger := watermill.NopLogger{}

	upstreamPubSub := gochannel.NewGoChannel(gochannel.Config{}, logger)
	upstreamTopic := "upstream-topic"

	router, err := message.NewRouter(message.RouterConfig{}, logger)
	require.NoError(t, err)

	fanout, err := gochannel.NewFanOut(router, upstreamPubSub, logger)
	require.NoError(t, err)

	fanout.AddSubscription(upstreamTopic)

	var counter uint64

	workersCount := 10
	messagesCount := 100

	for i := 0; i < workersCount; i++ {
		router.AddNoPublisherHandler(
			fmt.Sprintf("worker-%v", i),
			upstreamTopic,
			fanout,
			func(msg *message.Message) error {
				atomic.AddUint64(&counter, 1)
				return nil
			},
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*1)
	defer cancel()

	go func() {
		err := router.Run(ctx)
		require.NoError(t, err)
	}()

	<-router.Running()

	go func() {
		for i := 0; i < messagesCount; i++ {
			msg := message.NewMessage(watermill.NewUUID(), nil)
			err := upstreamPubSub.Publish(upstreamTopic, msg)
			if err != nil {
				panic(err)
			}
		}
	}()

	<-ctx.Done()

	require.Equal(t, uint64(workersCount*messagesCount), counter)
}

func TestFanOut_RouterRunning(t *testing.T) {
	logger := watermill.NopLogger{}
	pubSub := gochannel.NewGoChannel(gochannel.Config{}, logger)

	router, err := message.NewRouter(message.RouterConfig{}, logger)
	require.NoError(t, err)

	go func() {
		err := router.Run(context.Background())
		require.NoError(t, err)
	}()

	<-router.Running()

	_, err = gochannel.NewFanOut(router, pubSub, logger)
	require.Error(t, err)
}
