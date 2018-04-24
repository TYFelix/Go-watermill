package command

import (
	"github.com/roblaszczak/gooddd/_examples/enterprise-aggregate-with-events/shop/domain/cart"
	"github.com/pkg/errors"
	"github.com/roblaszczak/gooddd/domain"
)

type AddCart struct {
	CartID cart.ID
}

type AddCartHandler struct {
	repo          cart.Repository
	eventstore    domain.Eventstore
	eventsFactory domain.EventsFactory
}

func NewAddCartHandler(
	repo cart.Repository,
	eventstore domain.Eventstore,
	eventsFactory domain.EventsFactory,
) AddCartHandler {
	return AddCartHandler{repo, eventstore, eventsFactory}
}

func (h AddCartHandler) Handle(cmd AddCart) error {
	c := cart.NewCart(cmd.CartID)

	if err := h.repo.Save(c); err != nil {
		return errors.Wrap(err, "cannot save cart")
	}

	h.eventstore.Save(h.eventsFactory.NewEvents(c.PopEvents()))

	return nil
}
