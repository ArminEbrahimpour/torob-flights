# Adding a PNR checker (Go)

1. Create `services/api/internal/pnr/checkers/<airline>/`.
2. Add upstream client + mapper + `checker.go` implementing `pnr.Checker`.
3. Register in `services/api/cmd/api/main.go`:

```go
pnrRegistry := pnr.NewRegistry(
    mahan.NewChecker(mahanClient, location),
    newairline.NewChecker(...),
)
```

4. Add tests for mapper and verification rules.
5. No new HTTP service or deployable is required.
