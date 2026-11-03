package tickets

import "errors"

var ErrNotImplemented = errors.New("tickets service is not implemented")

type Service struct{}

func NewService() *Service { return &Service{} }
