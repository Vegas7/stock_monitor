package check

import (
	"context"
	"fmt"
	"strings"

	"github.com/Vegas7/stock_monitor/internal/config"
	"github.com/Vegas7/stock_monitor/internal/misaka"
)

type misakaChecker struct {
	it config.MisakaItem
	cl *misaka.Client
}

// NewMisaka returns a checker for one Misaka plan in one region.
func NewMisaka(it config.MisakaItem, cl *misaka.Client) Checker { return &misakaChecker{it, cl} }

func (m *misakaChecker) Check(ctx context.Context) (Result, error) {
	regions, err := m.cl.Regions(ctx)
	if err != nil {
		return Result{}, err
	}
	var region *misaka.Region
	for i := range regions {
		if strings.EqualFold(regions[i].Slug, m.it.Region) {
			region = &regions[i]
			break
		}
	}
	if region == nil {
		return Result{}, fmt.Errorf("region %q not in Misaka's region list: %w", m.it.Region, ErrFormatChanged)
	}
	if region.Available == nil {
		return Result{}, fmt.Errorf("region %q has no availability field: %w", m.it.Region, ErrFormatChanged)
	}
	plans, err := m.cl.Plans(ctx, region.ID)
	if err != nil {
		return Result{}, err
	}
	var plan *misaka.Plan
	for i := range plans {
		if strings.EqualFold(plans[i].Slug, m.it.Plan) {
			plan = &plans[i]
			break
		}
	}
	if plan == nil {
		return Result{}, fmt.Errorf("plan %q not in %s plan list: %w", m.it.Plan, strings.ToUpper(m.it.Region), ErrFormatChanged)
	}
	if plan.Available == nil {
		return Result{}, fmt.Errorf("plan %q has no availability field: %w", m.it.Plan, ErrFormatChanged)
	}
	if *region.Available && *plan.Available {
		name := plan.Name
		if name == "" {
			name = plan.Slug
		}
		return Result{Status: InStock, Detail: fmt.Sprintf("%s available in %s", name, strings.ToUpper(region.Slug))}, nil
	}
	reason := plan.UnavailableReason
	if !*plan.Available && reason == "" {
		reason = "unavailable"
	}
	if !*region.Available {
		reason = region.UnavailableReason
		if reason == "" {
			reason = "region unavailable"
		}
	}
	return Result{Status: OutOfStock, Detail: reason}, nil
}
