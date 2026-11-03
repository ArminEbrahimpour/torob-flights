package pnr

import "context"

type Checker interface {
	Airline() string
	Check(ctx context.Context, req VerifyRequest) (CheckResult, error)
}
