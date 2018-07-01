package middleware

import (
	"github.com/roblaszczak/gooddd/message/handler"
	"github.com/roblaszczak/gooddd/message"
)

const CorrelationIDMetadataKey = "correlation_id"

func CorrelationID(h handler.Func) handler.Func {
	return func(message message.Message) ([]message.Message, error) {
		producedMessages, err := h(message)

		correlationID := MessageCorrelationID(message)
		for _, msg := range producedMessages {
			SetCorrelationID(correlationID, msg)
		}

		return producedMessages, err
	}
}

func MessageCorrelationID(message message.Message) string {
	return message.GetMetadata(CorrelationIDMetadataKey)
}

func SetCorrelationID(id string, msg message.Message) {
	msg.SetMetadata(CorrelationIDMetadataKey, id)
}
