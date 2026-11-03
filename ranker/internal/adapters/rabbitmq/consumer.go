package rabbitmq

import (
	"context"
	"encoding/json"
	"github.com/rabbitmq/amqp091-go"
	"github.com/torob/ranker/internal/domain"
)

type Consumer struct{ url, exchange, queue, routing string }

func New(url, exchange, queue, routing string) *Consumer {
	return &Consumer{url: url, exchange: exchange, queue: queue, routing: routing}
}
func (c *Consumer) Consume(ctx context.Context, handler func(context.Context, domain.Flight) error) error {
	conn, err := amqp091.Dial(c.url)
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err = ch.ExchangeDeclare(c.exchange, amqp091.ExchangeTopic, true, false, false, false, nil); err != nil {
		return err
	}
	q, err := ch.QueueDeclare(c.queue, true, false, false, false, nil)
	if err != nil {
		return err
	}
	if err = ch.QueueBind(q.Name, c.routing, c.exchange, false, nil); err != nil {
		return err
	}
	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-msgs:
			if !ok {
				return context.Canceled
			}
			var flight domain.Flight
			if err := json.Unmarshal(msg.Body, &flight); err != nil {
				_ = msg.Nack(false, false)
				continue
			}
			if err := handler(ctx, flight); err != nil {
				_ = msg.Nack(false, true)
				return err
			}
			if err := msg.Ack(false); err != nil {
				return err
			}
		}
	}
}
