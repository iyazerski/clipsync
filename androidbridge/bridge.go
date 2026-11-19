// Package androidbridge exposes the shared Go client to the Android app.
package androidbridge

import (
	"context"
	"errors"
	"math"

	"github.com/iyazerski/clipsync/internal/client"
	"github.com/iyazerski/clipsync/internal/protocol"
)

const (
	MaxText = protocol.MaxText
	Offline = 1
	Refused = 2
)

func ValidatePairing(relay, secret string) (string, error) {
	address, err := client.NormalizeRelay(relay)
	if err != nil {
		return "", err
	}
	_, err = client.Token(secret)
	return address, err
}

func DeviceID() string { return "phone-" + client.RandomID(9) }

func Publish(relay, secret, device, text string) error {
	c, err := makeClient(relay, secret, device)
	if err != nil {
		return err
	}
	err = c.Publish(context.Background(), text)
	if errors.Is(err, client.ErrRefused) {
		return errors.New("the relay refused the secret: pair again")
	}
	return err
}

func makeClient(relay, secret, device string) (*client.Client, error) {
	address, err := client.NormalizeRelay(relay)
	if err != nil {
		return nil, err
	}
	return client.New(address, secret, device)
}

type Observer interface {
	OnConnected()
	OnClip(id int64, text, problem string)
}

type Receiver struct {
	receiver client.Receiver
}

func NewReceiver() *Receiver { return &Receiver{} }

type Subscription struct {
	client *client.Client
	ctx    context.Context
	cancel context.CancelFunc
}

func Subscribe(relay, secret, device string) (*Subscription, error) {
	c, err := makeClient(relay, secret, device)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Subscription{client: c, ctx: ctx, cancel: cancel}, nil
}

func (s *Subscription) Stop() { s.cancel() }

func (s *Subscription) Run(receiver *Receiver, seen int64, observer Observer) int {
	defer s.Stop()
	err := s.client.Stream(s.ctx, &receiver.receiver, uint64(max(0, seen)), observer.OnConnected, func(id uint64, text string, err error) {
		if id > math.MaxInt64 {
			id = 0
		}
		var problem string
		if err != nil {
			problem = err.Error()
		}
		observer.OnClip(int64(id), text, problem)
	})
	switch {
	case errors.Is(err, client.ErrRefused):
		return Refused
	case err != nil && s.ctx.Err() == nil:
		return Offline
	default:
		return 0
	}
}
