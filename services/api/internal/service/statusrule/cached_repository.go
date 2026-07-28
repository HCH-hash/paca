package statusrulesvc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	statusruledom "github.com/Paca-AI/api/internal/domain/statusrule"
	"github.com/Paca-AI/api/internal/platform/cache"
)

// CachedRepository decorates a statusruledom.Repository with a Redis-backed
// cache for ListEnabledRulesByProjectAndStatus — the hot read on every task
// status-change event, evaluated project-wide rather than gated by workflow
// membership — invalidated whenever a rule targeting that status is
// created, updated, deleted, or reordered through this same decorator.
//
// A single CachedRepository instance is shared between statusrulesvc.Service
// (HTTP writes) and both worker consumers that call ApplyMatchingRule (the
// project-wide status-rule consumer, and the automation-workflow
// predecessor-done cascade) — see bootstrap wiring — so a rule edited via
// the API is visible to the very next automation event, not just once some
// TTL expires.
//
// Cache errors are non-fatal: on a read error the decorator falls through
// to the real repository; on a write/delete error it logs and continues so
// mutations always succeed even when the cache is temporarily unavailable.
type CachedRepository struct {
	repo statusruledom.Repository
	st   *cache.Store
	ttl  time.Duration
	log  *slog.Logger
}

// NewCachedRepository wraps repo with a caching layer backed by st. ttl
// controls how long a cached rule list lives; zero disables caching. log
// receives non-fatal cache warnings.
func NewCachedRepository(repo statusruledom.Repository, st *cache.Store, ttl time.Duration, log *slog.Logger) *CachedRepository {
	return &CachedRepository{repo: repo, st: st, ttl: ttl, log: log}
}

func enabledRulesKey(projectID, statusID uuid.UUID) string {
	return fmt.Sprintf("statusrule:%s:%s:enabled-rules", projectID, statusID)
}

// ListEnabledRulesByProjectAndStatus returns (projectID, statusID)'s enabled
// rules, reading from cache when available and populating it on a miss.
func (c *CachedRepository) ListEnabledRulesByProjectAndStatus(ctx context.Context, projectID, statusID uuid.UUID) ([]*statusruledom.StatusAssignmentRule, error) {
	if c.ttl == 0 {
		return c.repo.ListEnabledRulesByProjectAndStatus(ctx, projectID, statusID)
	}
	key := enabledRulesKey(projectID, statusID)
	var result []*statusruledom.StatusAssignmentRule
	if ok, err := c.st.Get(ctx, key, &result); ok {
		return result, nil
	} else if err != nil {
		c.log.WarnContext(ctx, "cache: ListEnabledRulesByProjectAndStatus get", "err", err)
	}

	result, err := c.repo.ListEnabledRulesByProjectAndStatus(ctx, projectID, statusID)
	if err != nil {
		return nil, err
	}
	if err := c.st.Set(ctx, key, result, c.ttl); err != nil {
		c.log.WarnContext(ctx, "cache: ListEnabledRulesByProjectAndStatus set", "err", err)
	}
	return result, nil
}

// CreateRule delegates to the underlying repository and invalidates the
// cached rule list for r's (project, status).
func (c *CachedRepository) CreateRule(ctx context.Context, r *statusruledom.StatusAssignmentRule) error {
	if err := c.repo.CreateRule(ctx, r); err != nil {
		return err
	}
	c.invalidate(ctx, r.ProjectID, r.StatusID)
	return nil
}

// UpdateRule delegates to the underlying repository and invalidates the
// cached rule list for r's (project, status).
func (c *CachedRepository) UpdateRule(ctx context.Context, r *statusruledom.StatusAssignmentRule) error {
	if err := c.repo.UpdateRule(ctx, r); err != nil {
		return err
	}
	c.invalidate(ctx, r.ProjectID, r.StatusID)
	return nil
}

// DeleteRule looks up the rule first (the Repository interface only takes
// an ID) so it knows which (project, status) cache entry to invalidate once
// the delete succeeds.
func (c *CachedRepository) DeleteRule(ctx context.Context, id uuid.UUID) error {
	r, err := c.repo.FindRuleByID(ctx, id)
	if err != nil {
		return err
	}
	if err := c.repo.DeleteRule(ctx, id); err != nil {
		return err
	}
	c.invalidate(ctx, r.ProjectID, r.StatusID)
	return nil
}

// ReorderRules delegates to the underlying repository and invalidates the
// cached rule list for (projectID, statusID).
func (c *CachedRepository) ReorderRules(ctx context.Context, projectID, statusID uuid.UUID, ruleIDs []uuid.UUID) error {
	if err := c.repo.ReorderRules(ctx, projectID, statusID, ruleIDs); err != nil {
		return err
	}
	c.invalidate(ctx, projectID, statusID)
	return nil
}

func (c *CachedRepository) invalidate(ctx context.Context, projectID, statusID uuid.UUID) {
	if c.ttl == 0 {
		return
	}
	if err := c.st.Delete(ctx, enabledRulesKey(projectID, statusID)); err != nil {
		c.log.WarnContext(ctx, "cache: invalidate enabled rules", "err", err)
	}
}

// FindRuleByID delegates directly to the underlying repository (not cached).
func (c *CachedRepository) FindRuleByID(ctx context.Context, id uuid.UUID) (*statusruledom.StatusAssignmentRule, error) {
	return c.repo.FindRuleByID(ctx, id)
}

// ListRulesByProject delegates directly to the underlying repository (not cached).
func (c *CachedRepository) ListRulesByProject(ctx context.Context, projectID uuid.UUID) ([]*statusruledom.StatusAssignmentRule, error) {
	return c.repo.ListRulesByProject(ctx, projectID)
}

// StatusUsedByStatusRule delegates directly to the underlying repository (not cached).
func (c *CachedRepository) StatusUsedByStatusRule(ctx context.Context, statusID uuid.UUID) (bool, error) {
	return c.repo.StatusUsedByStatusRule(ctx, statusID)
}
