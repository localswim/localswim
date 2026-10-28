package workspace

import (
	"context"
	"sort"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

// Jobs are coalesced across callers, but never reused as current authority. A new
// poll requires a new authorized board snapshot. Incomplete/failed probes do not
// disclose old cached board facts after a revocation or identity replacement.
type catalogJob struct {
	key, channel string
	member       member
	engine       *L.Runtime
	done         chan struct{}
	result       L.CatalogSnapshot
	observed     time.Time
	err          error
	// joinGeneration is protected by r.mu. A published snapshot must begin
	// after every caller receiving it has joined this job.
	joinGeneration uint64
}

func (r *Runtime) catalog(ctx context.Context, channel string) T.WorkspaceCatalog {
	c := r.snapshot()
	jobs := make(map[string]*catalogJob, len(c.Members))
	// The pending queue is FIFO across polls and channels. Completion admits the
	// next pending member rather than restarting from the first configured member.
	r.mu.Lock()
	if r.catalogJobs == nil {
		r.catalogJobs = map[string]*catalogJob{}
	}
	for _, m := range c.Members {
		key := memberKey(m.ProjectKey, m.BoardKey)
		s := r.states[key]
		if r.closing || s == nil || s.engine == nil || s.availability != "ready" {
			continue
		}
		jk := channel + "/" + key
		j := r.catalogJobs[jk]
		if j == nil {
			j = &catalogJob{key: jk, channel: channel, member: m, engine: s.engine, done: make(chan struct{})}
			r.catalogJobs[jk] = j
			r.catalogPending = append(r.catalogPending, j)
		}
		j.joinGeneration++
		jobs[key] = j
	}
	r.pumpCatalogLocked()
	r.mu.Unlock()
	budget := time.NewTimer(2 * time.Second)
	defer budget.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		complete := true
		for _, j := range jobs {
			select {
			case <-j.done:
			default:
				complete = false
			}
		}
		if complete {
			break
		}
		select {
		case <-ctx.Done():
			break wait
		case <-budget.C:
			break wait
		case <-r.ctx.Done():
			break wait
		case <-tick.C:
			r.mu.Lock()
			r.pumpCatalogLocked()
			r.pumpActivationLocked()
			r.mu.Unlock()
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	out := T.WorkspaceCatalog{APIVersion: 1, WorkspaceID: c.WorkspaceID, ConfigRevision: c.ConfigRevision, ObservedAt: A.Timestamp(now.Format(time.RFC3339)), Entries: []T.WorkspaceCatalogEntry{}}
	for _, m := range c.Members {
		key := memberKey(m.ProjectKey, m.BoardKey)
		e := unknownCatalog(m)
		if p, ok := c.project(m.ProjectKey); ok {
			e.ProjectLabel = p.Label
		}
		r.mu.Lock()
		s := r.states[key]
		if s != nil {
			e.Availability = s.availability
		}
		r.mu.Unlock()
		if e.Availability == "pending" {
			e.Availability = "unavailable"
		}
		if j := jobs[key]; j != nil {
			select {
			case <-j.done:
				if j.err == nil {
					id, epoch := m.Identity, T.UUIDv4(j.engine.Epoch())
					observed := A.Timestamp(j.observed.Format(time.RFC3339))
					age := elapsed(now, j.observed)
					revision, count := j.result.Revision, j.result.ReadyForReview
					e.Identity, e.MemberEpoch, e.LastObservedAt, e.AgeSeconds, e.BoardRevision = &id, &epoch, &observed, &age, &revision
					e.Availability, e.Freshness = "ready", "fresh"
					e.Activity = T.CatalogActivity{State: "known", LastCommittedAt: j.result.LastCardActivity, Coverage: j.result.Coverage}
					e.Attention.ReadyForReview = T.CatalogCount{State: "known", Value: &count}
				} else {
					e.Availability = "unavailable"
				}
			default:
				e.Availability = "unavailable"
			}
		}
		out.Entries = append(out.Entries, e)
	}
	sort.SliceStable(out.Entries, func(i, j int) bool { return catalogLess(out.Entries[i], out.Entries[j]) })
	return out
}

// r.mu protects only queue bookkeeping. No native/SQL IO or wait holds it.
// The shared slot remains occupied until a native call really returns, even
// when its context or a catalog response has expired.
func (r *Runtime) pumpCatalogLocked() {
	for !r.closing && (len(r.catalogPending) > 0 || len(r.activationPending) > 0) {
		select {
		case r.slots <- struct{}{}:
		default:
			return
		}
		if len(r.activationPending) > 0 && (len(r.catalogPending) == 0 || r.preferActivation) {
			m := r.activationPending[0]
			r.activationPending = r.activationPending[1:]
			r.preferActivation = false
			r.jobs.Add(1)
			r.startActivation(m)
			continue
		}
		j := r.catalogPending[0]
		r.catalogPending = r.catalogPending[1:]
		r.preferActivation = true
		r.jobs.Add(1)
		go func() {
			defer r.jobs.Done()
			ctx, cancel := context.WithTimeout(r.ctx, time.Second)
			defer cancel()
			for {
				r.mu.Lock()
				generation := j.joinGeneration
				r.mu.Unlock()
				var result L.CatalogSnapshot
				err := ctx.Err()
				if err == nil {
					if r.catalogProbe != nil {
						result, err = r.catalogProbe(ctx, j.engine, j.channel)
					} else {
						result, err = j.engine.CatalogSnapshot(ctx, j.channel)
					}
				}
				r.mu.Lock()
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				if err == nil && generation != j.joinGeneration {
					// A late poll cannot inherit authority observed before it
					// joined. Re-read within this job's original attempt budget.
					r.mu.Unlock()
					continue
				}
				if err == nil {
					j.result = result
				}
				j.err = err
				j.observed = time.Now().UTC().Truncate(time.Second)
				<-r.slots
				delete(r.catalogJobs, j.key)
				close(j.done)
				r.pumpCatalogLocked()
				r.mu.Unlock()
				return
			}
		}()
	}
}

func unknownCatalog(m member) T.WorkspaceCatalogEntry {
	return T.WorkspaceCatalogEntry{ProjectKey: m.ProjectKey, BoardKey: m.BoardKey, Label: m.Label, Availability: "unavailable", Freshness: "never", Activity: T.CatalogActivity{State: "unknown"}, Attention: T.CatalogAttention{ReadyForReview: T.CatalogCount{State: "unknown"}, Blocked: T.CatalogCount{State: "not_configured"}, NeedsTerry: T.CatalogCount{State: "not_configured"}}}
}
func elapsed(now, at time.Time) A.Number {
	if now.Before(at) {
		return 0
	}
	return A.Number(now.Sub(at) / time.Second)
}
func catalogLess(a, b T.WorkspaceCatalogEntry) bool {
	aa, bb := a.Activity.LastCommittedAt, b.Activity.LastCommittedAt
	if aa != nil && bb == nil {
		return true
	}
	if aa == nil && bb != nil {
		return false
	}
	if aa != nil && bb != nil {
		if *aa != *bb {
			return *aa > *bb
		}
		ac, bc := a.Attention.ReadyForReview, b.Attention.ReadyForReview
		// Only compare supported known sums when both operands have fresh facts.
		ak := a.Freshness == "fresh" && ac.State == "known" && ac.Value != nil
		bk := b.Freshness == "fresh" && bc.State == "known" && bc.Value != nil
		if ak != bk {
			return ak
		}
		if ak && *ac.Value != *bc.Value {
			return *ac.Value > *bc.Value
		}
	}
	al, bl := catalogFold(string(a.Label)), catalogFold(string(b.Label))
	if al != bl {
		return al < bl
	}
	if a.ProjectKey != b.ProjectKey {
		return a.ProjectKey < b.ProjectKey
	}
	return a.BoardKey < b.BoardKey
}
