package pnr

import (
	"context"
	"fmt"
	"strings"
)

var ErrUnsupportedAirline = fmt.Errorf("unsupported airline")

type Registry struct {
	checkers map[string]Checker
}

func NewRegistry(checkers ...Checker) *Registry {
	m := make(map[string]Checker, len(checkers))
	for _, c := range checkers {
		m[normalizeAirline(c.Airline())] = c
	}
	return &Registry{checkers: m}
}

func (r *Registry) Airlines() []string {
	out := make([]string, 0, len(r.checkers))
	for airline := range r.checkers {
		out = append(out, airline)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (r *Registry) Check(ctx context.Context, airline string, req VerifyRequest) (CheckResult, error) {
	checker, ok := r.checkers[normalizeAirline(airline)]
	if !ok {
		return CheckResult{}, ErrUnsupportedAirline
	}
	return checker.Check(ctx, req)
}

func normalizeAirline(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	return value
}
