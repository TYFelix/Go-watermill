package middleware

import (
	"time"
	"github.com/rcrowley/go-metrics"
	"github.com/roblaszczak/gooddd/handler"
	"github.com/roblaszczak/gooddd/domain"
)

// todo - rewrite (more universal?)
type Metrics struct {
	timer   metrics.Timer
	errs    metrics.Counter
	success metrics.Counter
}

func NewMetrics(timer metrics.Timer, errs metrics.Counter, success metrics.Counter) Metrics {
	return Metrics{timer, errs, success}
}

func (m Metrics) Middleware(h handler.Handler) handler.Handler {
	return func(event domain.Event) (events []domain.EventPayload, err error) {
		start := time.Now()
		defer func() {
			m.timer.Update(time.Now().Sub(start))
			if err != nil {
				m.errs.Inc(1)
			} else {
				m.success.Inc(1)
			}
		}()

		return h(event)
	}
}

func (m Metrics) ShowStats(interval time.Duration, logger metrics.Logger) {
	go metrics.Log(metrics.DefaultRegistry, interval, logger)
}
