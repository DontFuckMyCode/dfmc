package provider

// race.go — CompleteRaced and supporting target resolver. Issues
// req against every candidate concurrently and returns the first
// successful response; cancels the losers as soon as a winner is
// declared. Pays N× the token bill, so it's NOT a drop-in for
// Complete; intended for latency-sensitive paths or for tolerating
// a flaky-but-fast primary alongside a slow-but-reliable fallback.

import (
	"context"
	"errors"
	"fmt"
)

// CompleteRaced issues req against every candidate concurrently and returns
// the first successful response. The losing calls are cancelled as soon as a
// winner is declared. If every candidate errors, the joined error is returned.
//
// When candidates is empty, the router derives them from ResolveOrder but
// strips "offline" — racing the offline stub is pointless because it always
// answers instantly with a canned analyzer response. If stripping leaves no
// candidates (e.g. only offline is configured), offline is kept so the call
// still returns something.
//
// Use this when latency or reliability matters more than cost — e.g. you have
// a fast-but-flaky primary and a slow-but-reliable fallback and want the best
// of both. It is NOT a drop-in replacement for Complete: it pays N× the token
// bill per call.
func (r *Router) CompleteRaced(ctx context.Context, req CompletionRequest, candidates []string) (*CompletionResponse, string, error) {
	targets := r.resolveRaceTargets(req.Provider, candidates)
	if len(targets) == 0 {
		return nil, "", fmt.Errorf("%w: no candidates for race", ErrProviderNotFound)
	}
	if len(targets) == 1 {
		resp, err := targets[0].p.Complete(ctx, req)
		if err != nil {
			return nil, targets[0].p.Name(), err
		}
		return resp, targets[0].p.Name(), nil
	}

	type result struct {
		resp *CompletionResponse
		name string
		err  error
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make(chan result, len(targets))
	for _, t := range targets {
		go func(t raceTarget) {
			resp, err := t.p.Complete(raceCtx, req)
			out <- result{resp: resp, name: t.p.Name(), err: err}
		}(t)
	}
	var errs []error
	for range targets {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case r := <-out:
			if r.err == nil {
				return r.resp, r.name, nil
			}
			errs = append(errs, fmt.Errorf("%s: %w", r.name, r.err))
		}
	}
	return nil, "", errors.Join(errs...)
}

type raceTarget struct {
	name string
	p    Provider
}

// resolveRaceTargets normalises/dedupes the candidate list and resolves each
// name to a concrete provider. Unknown names are silently dropped — the
// alternative (hard error) would make the race abort on a single typo even
// when viable providers are still in the list.
func (r *Router) resolveRaceTargets(requested string, candidates []string) []raceTarget {
	if len(candidates) == 0 {
		order := r.ResolveOrder(requested)
		for _, name := range order {
			if name != "offline" {
				candidates = append(candidates, name)
			}
		}
		if len(candidates) == 0 {
			candidates = order
		}
	}
	seen := map[string]struct{}{}
	var targets []raceTarget
	for _, c := range candidates {
		n := normalizeProviderName(c)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		p, ok := r.Get(n)
		if !ok {
			continue
		}
		targets = append(targets, raceTarget{name: n, p: p})
	}
	return targets
}
