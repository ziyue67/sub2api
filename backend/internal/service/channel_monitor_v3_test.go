package service

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// monitorV3RepoFake keeps V3 state in memory and serves canned passive facts.
type monitorV3RepoFake struct {
	mu         sync.Mutex
	config     ChannelMonitorV3Config
	categories []ChannelMonitorV3Category
	components []ChannelMonitorV3Component
	slotFacts  ChannelMonitorV3Facts
	totals     ChannelMonitorV3Facts
	through    *time.Time
	slotCalls  []monitorV3SlotCall
	nextID     int64
}

type monitorV3SlotCall struct {
	groups      []int64
	from, to    time.Time
	interval    time.Duration
	withLatency bool
}

func (r *monitorV3RepoFake) id() int64 { r.nextID++; return r.nextID }

func (r *monitorV3RepoFake) GetConfig(context.Context) (*ChannelMonitorV3Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg := r.config
	return &cfg, nil
}
func (r *monitorV3RepoFake) UpdateConfig(_ context.Context, cfg ChannelMonitorV3Config, version int, by int64) (*ChannelMonitorV3Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if version != r.config.Version {
		return nil, nil
	}
	cfg.Version, cfg.UpdatedBy = r.config.Version+1, &by
	r.config = cfg
	return &cfg, nil
}
func (r *monitorV3RepoFake) ListCategories(context.Context) ([]ChannelMonitorV3Category, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]ChannelMonitorV3Category(nil), r.categories...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out, nil
}
func (r *monitorV3RepoFake) CreateCategory(_ context.Context, in ChannelMonitorV3CategoryInput) (*ChannelMonitorV3Category, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := ChannelMonitorV3Category{ID: r.id(), Name: in.Name, Description: in.Description, SortOrder: len(r.categories)}
	r.categories = append(r.categories, c)
	return &c, nil
}
func (r *monitorV3RepoFake) UpdateCategory(_ context.Context, id int64, in ChannelMonitorV3CategoryInput) (*ChannelMonitorV3Category, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.categories {
		if r.categories[i].ID == id {
			r.categories[i].Name, r.categories[i].Description = in.Name, in.Description
			c := r.categories[i]
			return &c, nil
		}
	}
	return nil, nil
}
func (r *monitorV3RepoFake) DeleteCategory(_ context.Context, id int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.categories {
		if r.categories[i].ID == id {
			r.categories = append(r.categories[:i], r.categories[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (r *monitorV3RepoFake) ListComponents(context.Context) ([]ChannelMonitorV3Component, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]ChannelMonitorV3Component(nil), r.components...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out, nil
}
func (r *monitorV3RepoFake) GetComponent(_ context.Context, id int64) (*ChannelMonitorV3Component, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.components {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, nil
}
func (r *monitorV3RepoFake) CreateComponent(_ context.Context, in ChannelMonitorV3ComponentInput) (*ChannelMonitorV3Component, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := ChannelMonitorV3Component{ID: r.id(), CategoryID: in.CategoryID, Name: in.Name, Description: in.Description, GroupID: in.GroupID,
		Model: in.Model, DegradedTTFTMs: in.DegradedTTFTMs, ShowMultiplier: in.ShowMultiplier, Visibility: in.Visibility, Enabled: in.Enabled,
		SortOrder: len(r.components)}
	r.components = append(r.components, c)
	return &c, nil
}
func (r *monitorV3RepoFake) UpdateComponent(context.Context, int64, ChannelMonitorV3ComponentInput) (*ChannelMonitorV3Component, error) {
	return nil, nil
}
func (r *monitorV3RepoFake) DeleteComponent(_ context.Context, id int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.components {
		if r.components[i].ID == id {
			r.components = append(r.components[:i], r.components[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (r *monitorV3RepoFake) Reorder(context.Context, []int64, []int64) error { return nil }
func (r *monitorV3RepoFake) SlotFacts(_ context.Context, groups []int64, from, to time.Time, interval time.Duration, withLatency bool) (*ChannelMonitorV3Facts, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slotCalls = append(r.slotCalls, monitorV3SlotCall{groups: groups, from: from, to: to, interval: interval, withLatency: withLatency})
	out := ChannelMonitorV3Facts{}
	inRange := func(slot time.Time) bool { return !slot.Before(from) && slot.Before(to) }
	for _, f := range r.slotFacts.Metrics {
		if inRange(f.Slot) {
			out.Metrics = append(out.Metrics, f)
		}
	}
	for _, f := range r.slotFacts.Errors {
		if inRange(f.Slot) {
			out.Errors = append(out.Errors, f)
		}
	}
	if withLatency {
		for _, f := range r.slotFacts.Latency {
			if inRange(f.Slot) {
				out.Latency = append(out.Latency, f)
			}
		}
	}
	return &out, nil
}
func (r *monitorV3RepoFake) RangeFacts(context.Context, []int64, time.Time, time.Time) (*ChannelMonitorV3Facts, error) {
	totals := r.totals
	return &totals, nil
}
func (r *monitorV3RepoFake) DataThrough(context.Context) (*time.Time, error) { return r.through, nil }

var monitorV3Now = time.Date(2026, 10, 4, 9, 31, 20, 0, time.UTC)

func monitorV3Config() ChannelMonitorV3Config {
	return ChannelMonitorV3Config{Version: 1, IntervalMinutes: 5, Cells: 30, AvailabilityRange: "7d", DownErrorRate: 0.2,
		DegradedErrorRate: 0.05, DegradedTTFTMs: 10000, MinRequests: 1, IgnoredErrorCategories: append([]string(nil), DefaultChannelMonitorV2IgnoredErrorCategories...)}
}

func monitorV3Service(repo *monitorV3RepoFake) *ChannelMonitorV3Service {
	s := NewChannelMonitorV3Service(repo, groupTestGroupsFake{4: {ID: 4, Status: StatusActive}, 5: {ID: 5, Status: StatusActive}})
	s.now = func() time.Time { return monitorV3Now }
	return s
}

func monitorV3ID(v int64) *int64 { return &v }

// monitorV3Slot is the start of the n-th slot before the current one.
func monitorV3Slot(back int) time.Time {
	return monitorV3Now.Truncate(5 * time.Minute).Add(-time.Duration(back) * 5 * time.Minute)
}

func TestChannelMonitorV3ClassifiesSlotsFromRealTraffic(t *testing.T) {
	cfg := monitorV3Config()
	cfg.MinRequests = 5
	component := ChannelMonitorV3Component{ID: 1, GroupID: 4}
	first := monitorV3Slot(7)
	slot := func(i int) time.Time { return first.Add(time.Duration(i) * 5 * time.Minute) }
	facts := &ChannelMonitorV3Facts{
		Metrics: []ChannelMonitorV3Fact{
			{GroupID: 4, Model: "gpt", Slot: slot(0), Success: 98, Errors: 2},
			{GroupID: 4, Model: "gpt", Slot: slot(1), Success: 90, Errors: 10},
			{GroupID: 4, Model: "gpt", Slot: slot(2), Success: 70, Errors: 30},
			{GroupID: 4, Model: "gpt", Slot: slot(3), Success: 100},
			{GroupID: 4, Model: "gpt", Slot: slot(4), Success: 10, Errors: 20},
			{GroupID: 4, Model: "gpt", Slot: slot(6), Success: 1},
			{GroupID: 9, Model: "gpt", Slot: slot(5), Success: 0, Errors: 50},
		},
		Errors: []ChannelMonitorV3ErrorFact{
			{GroupID: 4, Model: "gpt", Slot: slot(2), Category: "upstream_5xx", Errors: 20},
			{GroupID: 4, Model: "gpt", Slot: slot(2), Category: "timeout", Errors: 10},
			{GroupID: 4, Model: "gpt", Slot: slot(4), Category: "authentication", Errors: 15},
			{GroupID: 4, Model: "gpt", Slot: slot(4), Category: "client_cancelled", Errors: 5},
		},
		Latency: []ChannelMonitorV3LatencyFact{
			{GroupID: 4, Model: "gpt", Slot: slot(0), UpperBound: 2000, Count: 98},
			{GroupID: 4, Model: "gpt", Slot: slot(3), UpperBound: 10000, Count: 40},
			{GroupID: 4, Model: "gpt", Slot: slot(3), UpperBound: 15000, Count: 60},
		},
	}
	cells := channelMonitorV3Cells(cfg, component, facts, first, 8, 5*time.Minute, true)
	statuses := make([]string, len(cells))
	for i, cell := range cells {
		if cell != nil {
			statuses[i] = cell.Status
		}
	}
	require.Equal(t, []string{
		ChannelMonitorV3StatusOperational, // 2% errors
		ChannelMonitorV3StatusDegraded,    // 10% errors
		ChannelMonitorV3StatusDown,        // 30% errors
		ChannelMonitorV3StatusDegraded,    // P50 in the 15 s bucket
		ChannelMonitorV3StatusOperational, // every error was caused by users
		"",                                // only another group had traffic
		ChannelMonitorV3StatusInsufficient,
		"",
	}, statuses)
	require.Equal(t, "upstream_5xx", cells[2].TopError)
	require.InDelta(t, 0.7, cells[2].SuccessRate, 1e-9)
	require.Equal(t, int64(2000), *cells[0].TTFTP50Ms)
	require.Equal(t, int64(15000), *cells[3].TTFTP50Ms)
	require.Equal(t, int64(10), cells[4].Requests, "ignored errors are not counted requests")
	require.Equal(t, int64(20), cells[4].IgnoredErrors)
	require.Equal(t, cells[0].Start, first)

	// A per-component threshold above the P50 bucket keeps the slot green.
	component.DegradedTTFTMs = 15000
	require.Equal(t, ChannelMonitorV3StatusOperational, channelMonitorV3Cells(cfg, component, facts, first, 8, 5*time.Minute, false)[3].Status)
	// Users never get volumes.
	require.Zero(t, channelMonitorV3Cells(cfg, component, facts, first, 8, 5*time.Minute, false)[2].Requests)
}

func TestChannelMonitorV3ModelFilter(t *testing.T) {
	cfg := monitorV3Config()
	first := monitorV3Slot(0)
	facts := &ChannelMonitorV3Facts{Metrics: []ChannelMonitorV3Fact{
		{GroupID: 4, Model: "gpt-5.5", Slot: first, Success: 10},
		{GroupID: 4, Model: "gpt-5.5-mini", Slot: first, Success: 0, Errors: 10},
	}}
	whole := channelMonitorV3Cells(cfg, ChannelMonitorV3Component{GroupID: 4}, facts, first, 1, 5*time.Minute, true)[0]
	require.Equal(t, ChannelMonitorV3StatusDown, whole.Status)
	require.Equal(t, int64(20), whole.Requests)
	one := channelMonitorV3Cells(cfg, ChannelMonitorV3Component{GroupID: 4, Model: "gpt-5.5"}, facts, first, 1, 5*time.Minute, true)[0]
	require.Equal(t, ChannelMonitorV3StatusOperational, one.Status)
	require.Equal(t, int64(10), one.Requests)
}

func TestChannelMonitorV3QuietComponentDoesNotKeepOldFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		back int
		want string
	}{
		{"current", 0, ChannelMonitorV3StatusDown},
		{"brief lull", 2, ChannelMonitorV3StatusDown},
		{"old failure", 6, ChannelMonitorV3StatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := monitorV3StatusFixture()
			repo.slotFacts = ChannelMonitorV3Facts{Metrics: []ChannelMonitorV3Fact{
				{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(tc.back), Errors: 10},
			}}
			page, err := monitorV3Service(repo).Status(context.Background(), ChannelMonitorV3Viewer{AllowedGroups: map[int64]bool{4: true}}, nil)
			require.NoError(t, err)
			item := page.Categories[0].Components[0]
			require.Equal(t, tc.want, item.Status)
			require.Equal(t, ChannelMonitorV3StatusDown, item.Cells[29-tc.back].Status, "history is retained")
			require.Equal(t, monitorV3Slot(tc.back), *item.LastDataAt)
			require.NotNil(t, item.Availability, "historical request availability remains available")
		})
	}
}

func monitorV3StatusFixture() *monitorV3RepoFake {
	repo := &monitorV3RepoFake{config: monitorV3Config()}
	through := monitorV3Now.Add(-time.Minute)
	repo.through = &through
	repo.categories = []ChannelMonitorV3Category{{ID: 1, Name: "GPT", SortOrder: 1}, {ID: 2, Name: "Claude", SortOrder: 0}, {ID: 3, Name: "Empty", SortOrder: 2}}
	repo.components = []ChannelMonitorV3Component{
		{ID: 10, CategoryID: monitorV3ID(1), Name: "Codex", GroupID: 4, Enabled: true, ShowMultiplier: true, GroupRateMultiplier: 0.2, Visibility: ChannelMonitorV3VisibilityGroup},
		{ID: 11, CategoryID: monitorV3ID(1), Name: "Public", GroupID: 5, Enabled: true, Visibility: ChannelMonitorV3VisibilityPublic, SortOrder: 1},
		{ID: 12, CategoryID: monitorV3ID(2), Name: "Exclusive", GroupID: 6, Enabled: true, Visibility: ChannelMonitorV3VisibilityGroup, SortOrder: 2},
		{ID: 13, Name: "Featured only", GroupID: 5, Enabled: true, Visibility: ChannelMonitorV3VisibilityPublic, ShowMultiplier: true, GroupRateMultiplier: 0.9, SortOrder: 3},
		{ID: 14, CategoryID: monitorV3ID(3), Name: "Disabled", GroupID: 5, Enabled: false, Visibility: ChannelMonitorV3VisibilityPublic, SortOrder: 4},
	}
	repo.config.FeaturedComponentID = monitorV3ID(13)
	repo.slotFacts = ChannelMonitorV3Facts{
		Metrics: []ChannelMonitorV3Fact{
			{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(0), Success: 5, Errors: 5},
			{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(1), Success: 50},
			{GroupID: 5, Model: "claude", Slot: monitorV3Slot(0), Success: 40},
			{GroupID: 5, Model: "claude", Slot: monitorV3Slot(45), Success: 40},
		},
		Errors: []ChannelMonitorV3ErrorFact{{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(0), Category: "upstream_5xx", Errors: 5}},
	}
	repo.totals = ChannelMonitorV3Facts{
		Metrics: []ChannelMonitorV3Fact{{GroupID: 4, Model: "gpt", Success: 800, Errors: 250}, {GroupID: 5, Model: "claude", Success: 1000}},
		Errors:  []ChannelMonitorV3ErrorFact{{GroupID: 4, Model: "gpt", Category: "upstream_5xx", Errors: 200}, {GroupID: 4, Model: "gpt", Category: "client_cancelled", Errors: 50}},
	}
	return repo
}

func TestChannelMonitorV3StatusForUserScopesAndRedacts(t *testing.T) {
	repo := monitorV3StatusFixture()
	s := monitorV3Service(repo)
	status, err := s.Status(context.Background(), ChannelMonitorV3Viewer{AllowedGroups: map[int64]bool{4: true}, GroupRates: map[int64]float64{4: 0.15}}, nil)
	require.NoError(t, err)
	require.True(t, status.Window.Latest)
	require.Equal(t, monitorV3Slot(29), status.Window.Start)
	require.True(t, status.Window.HasOlder)
	require.Equal(t, monitorV3Now.Add(-7*24*time.Hour).Truncate(time.Hour), status.AvailabilitySince)
	require.Equal(t, repo.through, status.DataThrough)

	require.Len(t, status.Categories, 1, "categories without visible components are hidden")
	gpt := status.Categories[0]
	require.Len(t, gpt.Components, 2)
	codex := gpt.Components[0]
	require.Equal(t, ChannelMonitorV3StatusDown, codex.Status, "the latest judged slot decides the dot")
	require.InDelta(t, 80.0, *codex.Availability, 1e-9, "800 answered of 1000 counted, cancellations excluded")
	require.Zero(t, codex.Requests, "volumes are admin-only")
	require.InDelta(t, 0.15, *codex.Multiplier, 1e-9, "a user sees their own multiplier")
	require.Zero(t, codex.GroupID)
	require.Len(t, codex.Cells, 30)
	require.Equal(t, ChannelMonitorV3StatusDown, codex.Cells[29].Status)
	require.Equal(t, "upstream_5xx", codex.Cells[29].TopError)
	require.Zero(t, codex.Cells[29].Requests)
	require.Equal(t, ChannelMonitorV3StatusOperational, codex.Cells[28].Status)
	require.Nil(t, codex.Cells[0])
	require.Nil(t, gpt.Components[1].Multiplier, "multiplier can be hidden")
	require.InDelta(t, 90.0, *gpt.Availability, 1e-9, "category availability is the mean of its components")

	require.NotNil(t, status.Featured)
	require.Equal(t, "Featured only", status.Featured.Name)
	require.Zero(t, status.OpenIncidents, "one red slot is not an incident yet")
	require.True(t, repo.slotCalls[0].withLatency)
	require.ElementsMatch(t, []int64{4, 5}, repo.slotCalls[0].groups, "facts are read only for visible groups")

	viewer := ChannelMonitorV3Viewer{AllowedGroups: map[int64]bool{4: true}}
	repo.slotFacts.Metrics[1] = ChannelMonitorV3Fact{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(1), Errors: 50}
	status, err = s.Status(context.Background(), viewer, nil)
	require.NoError(t, err)
	require.Zero(t, status.OpenIncidents, "two red slots, the newer still filling up: red for six minutes so far")
	repo.slotFacts.Metrics = append(repo.slotFacts.Metrics, ChannelMonitorV3Fact{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(2), Errors: 50})
	status, err = s.Status(context.Background(), viewer, nil)
	require.NoError(t, err)
	require.Equal(t, 1, status.OpenIncidents, "ten minutes of red open an incident")
}

func TestChannelMonitorV3StatusOlderWindowAndHistoryLimit(t *testing.T) {
	repo := monitorV3StatusFixture()
	s := monitorV3Service(repo)
	end := monitorV3Slot(30)
	status, err := s.Status(context.Background(), ChannelMonitorV3Viewer{Admin: true}, &end)
	require.NoError(t, err)
	require.False(t, status.Window.Latest)
	require.Equal(t, monitorV3Slot(59), status.Window.Start)
	require.Len(t, status.Categories, 2, "admins see every enabled component")
	require.Equal(t, "Claude", status.Categories[0].Name, "categories follow sort order")
	public := status.Categories[1].Components[1]
	require.Equal(t, ChannelMonitorV3StatusOperational, public.Cells[14].Status, "the older window shows older slots")
	require.Equal(t, int64(40), public.Cells[14].Requests)
	require.NotNil(t, status.Featured)
	require.Equal(t, ChannelMonitorV3StatusOperational, status.Featured.Cells[29].Status, "the featured card stays on the current window")
	require.Zero(t, status.OpenIncidents, "only the live window counts open incidents")

	ancient := monitorV3Now.Add(-30 * 24 * time.Hour)
	status, err = s.Status(context.Background(), ChannelMonitorV3Viewer{Admin: true}, &ancient)
	require.NoError(t, err)
	require.False(t, status.Window.HasOlder)
	require.False(t, status.Window.Start.Before(monitorV3Now.Add(-ChannelMonitorV3History)), "paging stops at the facts V2 keeps")

	future := monitorV3Now.Add(time.Hour)
	status, err = s.Status(context.Background(), ChannelMonitorV3Viewer{Admin: true}, &future)
	require.NoError(t, err)
	require.True(t, status.Window.Latest)
	require.Equal(t, int64(4), status.Categories[1].Components[0].GroupID)
	require.Equal(t, int64(10), status.Categories[1].Components[0].Cells[29].Requests)
}

func TestChannelMonitorV3IncidentsFromDownRuns(t *testing.T) {
	repo := monitorV3StatusFixture()
	down := func(back int) ChannelMonitorV3Fact {
		return ChannelMonitorV3Fact{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(back), Errors: 10}
	}
	up := func(back int) ChannelMonitorV3Fact {
		return ChannelMonitorV3Fact{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(back), Success: 10}
	}
	repo.slotFacts = ChannelMonitorV3Facts{Metrics: []ChannelMonitorV3Fact{
		down(20), down(19), up(18), // resolved after two slots
		up(16), down(15), up(14), // five minutes of red is only a blip
		down(11), down(10), up(8), // ends with its last down slot, before the quiet one
		down(2), down(1), down(0), // ongoing for eleven minutes
		{GroupID: 5, Model: "claude", Slot: monitorV3Slot(4), Errors: 3},
		{GroupID: 5, Model: "claude", Slot: monitorV3Slot(3), Errors: 3},
	}, Errors: []ChannelMonitorV3ErrorFact{{GroupID: 4, Model: "gpt", Slot: monitorV3Slot(0), Category: "rate_or_capacity", Errors: 10}}}
	s := monitorV3Service(repo)

	page, err := s.Incidents(context.Background(), ChannelMonitorV3Viewer{AllowedGroups: map[int64]bool{4: true}}, 1, 20)
	require.NoError(t, err)
	require.False(t, repo.slotCalls[0].withLatency, "incidents do not need latency")
	require.Equal(t, 5, page.Total, "three Codex runs, and the Claude run on both components of the public group")
	ongoing := page.Items[0]
	require.Nil(t, ongoing.EndedAt)
	require.Equal(t, "Codex", ongoing.ComponentName)
	require.Equal(t, monitorV3Slot(2), ongoing.StartedAt)
	require.Equal(t, 3, ongoing.DownSlots)
	require.Equal(t, "rate_or_capacity", ongoing.TopError)
	require.Zero(t, ongoing.Requests)
	resolved := page.Items[len(page.Items)-1]
	require.Equal(t, monitorV3Slot(20), resolved.StartedAt)
	require.Equal(t, monitorV3Slot(18), *resolved.EndedAt)
	require.Equal(t, 2, resolved.DownSlots)
	quiet := page.Items[len(page.Items)-2]
	require.Equal(t, monitorV3Slot(11), quiet.StartedAt)
	require.Equal(t, monitorV3Slot(9), *quiet.EndedAt, "the run ends where the down slot ends")
	for _, item := range page.Items {
		require.NotEqual(t, monitorV3Slot(15), item.StartedAt, "a single red slot is not listed")
	}

	page, err = s.Incidents(context.Background(), ChannelMonitorV3Viewer{AllowedGroups: map[int64]bool{}}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 2, page.Total, "only the public components remain")
	page, err = s.Incidents(context.Background(), ChannelMonitorV3Viewer{Admin: true}, 1, 3)
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	require.Equal(t, int64(30), page.Items[0].Errors, "admins get volumes")
	page, err = s.Incidents(context.Background(), ChannelMonitorV3Viewer{Admin: true}, 2, 3)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)

	// Without its first slot the ongoing run has been red for six minutes only.
	repo.slotFacts.Metrics = slices.DeleteFunc(repo.slotFacts.Metrics, func(f ChannelMonitorV3Fact) bool {
		return f.GroupID == 4 && f.Slot.Equal(monitorV3Slot(2))
	})
	page, err = s.Incidents(context.Background(), ChannelMonitorV3Viewer{AllowedGroups: map[int64]bool{4: true}}, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 4, page.Total, "an ongoing run is listed once it has been red for ten minutes")
}

func TestChannelMonitorV3IncidentsBridgeQuietSlots(t *testing.T) {
	cells := func(statuses map[int]string) []*ChannelMonitorV3Cell {
		out := make([]*ChannelMonitorV3Cell, 41)
		for back, status := range statuses {
			out[40-back] = &ChannelMonitorV3Cell{Start: monitorV3Slot(back), Status: status}
		}
		return out
	}
	down, up, quiet := ChannelMonitorV3StatusDown, ChannelMonitorV3StatusOperational, ChannelMonitorV3StatusInsufficient
	component := ChannelMonitorV3Component{ID: 1, Name: "Codex"}

	runs := channelMonitorV3RunsFromCells(component, cells(map[int]string{
		40: down, 38: down, 37: up, // a quiet slot in between does not split an outage
		30: down, 29: down, 25: quiet, 19: down, 18: down, 17: up, // 45 minutes of quiet does
		3: down, 1: down, // ongoing across a quiet slot
	}), 5*time.Minute, monitorV3Now, true)
	require.Len(t, runs, 4)
	require.Equal(t, monitorV3Slot(40), runs[0].StartedAt)
	require.Equal(t, monitorV3Slot(37), *runs[0].EndedAt)
	require.Equal(t, 2, runs[0].DownSlots)
	require.Equal(t, monitorV3Slot(28), *runs[1].EndedAt, "ends with its last down slot")
	require.Equal(t, monitorV3Slot(19), runs[2].StartedAt)
	require.Nil(t, runs[3].EndedAt)
	require.Equal(t, monitorV3Slot(3), runs[3].StartedAt)

	runs = channelMonitorV3RunsFromCells(component, cells(map[int]string{20: down, 19: down}), 5*time.Minute, monitorV3Now, true)
	require.Len(t, runs, 1)
	require.Equal(t, monitorV3Slot(18), *runs[0].EndedAt, "an hour and a half of silence since does not keep it open")
}

func TestChannelMonitorV3HidesUnusableGroupsFromUsers(t *testing.T) {
	public := ChannelMonitorV3Component{GroupID: 7, Visibility: ChannelMonitorV3VisibilityPublic, GroupStatus: StatusActive}
	user := ChannelMonitorV3Viewer{AllowedGroups: map[int64]bool{7: true}}
	require.True(t, user.canSee(public))
	deleted, disabled := public, public
	deleted.GroupDeleted = true
	disabled.GroupStatus = StatusDisabled
	require.False(t, user.canSee(deleted), "a deleted group has nothing to show")
	require.False(t, user.canSee(disabled), "users cannot use a disabled group")
	require.True(t, ChannelMonitorV3Viewer{Admin: true}.canSee(disabled), "admins still see it, flagged as unavailable")
}

func TestChannelMonitorV3Validation(t *testing.T) {
	repo := &monitorV3RepoFake{config: monitorV3Config(), categories: []ChannelMonitorV3Category{{ID: 1, Name: "GPT"}}}
	s := monitorV3Service(repo)
	ctx := context.Background()
	valid := ChannelMonitorV3ComponentInput{Name: " Codex ", GroupID: 4, Model: " gpt-5.5 ", CategoryID: monitorV3ID(1), Enabled: true}
	created, err := s.CreateComponent(ctx, valid)
	require.NoError(t, err)
	require.Equal(t, "Codex", created.Name)
	require.Equal(t, "gpt-5.5", created.Model)
	require.Equal(t, ChannelMonitorV3VisibilityGroup, created.Visibility, "components default to group visibility")
	for name, mutate := range map[string]func(*ChannelMonitorV3ComponentInput){
		"name":       func(in *ChannelMonitorV3ComponentInput) { in.Name = "" },
		"group":      func(in *ChannelMonitorV3ComponentInput) { in.GroupID = 999 },
		"category":   func(in *ChannelMonitorV3ComponentInput) { in.CategoryID = monitorV3ID(9) },
		"threshold":  func(in *ChannelMonitorV3ComponentInput) { in.DegradedTTFTMs = 12345 },
		"visibility": func(in *ChannelMonitorV3ComponentInput) { in.Visibility = "everyone" },
	} {
		input := valid
		mutate(&input)
		_, err := s.CreateComponent(ctx, input)
		require.ErrorIs(t, err, ErrChannelMonitorV3Invalid, name)
	}

	cfg := monitorV3Config()
	cfg.FeaturedComponentID = monitorV3ID(created.ID)
	cfg.IgnoredErrorCategories = []string{"timeout", "timeout"}
	updated, err := s.UpdateConfig(ctx, cfg, 7)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	require.Equal(t, []string{"timeout"}, updated.IgnoredErrorCategories)
	_, err = s.UpdateConfig(ctx, cfg, 7)
	require.ErrorIs(t, err, ErrChannelMonitorV3Conflict, "a stale version is rejected")
	cfg.Version, cfg.FeaturedComponentID = updated.Version, monitorV3ID(404)
	_, err = s.UpdateConfig(ctx, cfg, 7)
	require.ErrorIs(t, err, ErrChannelMonitorV3Invalid)
	for name, mutate := range map[string]func(*ChannelMonitorV3Config){
		"interval":   func(c *ChannelMonitorV3Config) { c.IntervalMinutes = 7 },
		"cells":      func(c *ChannelMonitorV3Config) { c.Cells = 500 },
		"range":      func(c *ChannelMonitorV3Config) { c.AvailabilityRange = "90d" },
		"rates":      func(c *ChannelMonitorV3Config) { c.DegradedErrorRate = 0.5 },
		"down rate":  func(c *ChannelMonitorV3Config) { c.DownErrorRate = 0 },
		"ttft":       func(c *ChannelMonitorV3Config) { c.DegradedTTFTMs = 9999 },
		"min":        func(c *ChannelMonitorV3Config) { c.MinRequests = 0 },
		"categories": func(c *ChannelMonitorV3Config) { c.IgnoredErrorCategories = []string{"bad_luck"} },
	} {
		next := monitorV3Config()
		mutate(&next)
		require.ErrorIs(t, normalizeChannelMonitorV3Config(&next), ErrChannelMonitorV3Invalid, name)
	}
	require.ErrorIs(t, s.DeleteComponent(ctx, 404), ErrChannelMonitorV3NotFound)
	_, err = s.CreateCategory(ctx, ChannelMonitorV3CategoryInput{Name: " "})
	require.ErrorIs(t, err, ErrChannelMonitorV3Invalid)
}

func TestChannelMonitorV3Modes(t *testing.T) {
	repo := &settingRepoForMonitorMode{values: map[string]string{}}
	notified := 0
	svc := &SettingService{settingRepo: repo}
	svc.SubscribeChannelMonitorRuntime(func() { notified++ })
	mode, err := svc.SetChannelMonitorMode(context.Background(), " V3 ")
	require.NoError(t, err)
	require.Equal(t, ChannelMonitorModeV3, mode)
	require.Equal(t, ChannelMonitorModeV3, repo.values[SettingKeyChannelMonitorMode])
	require.Equal(t, 1, notified, "the aggregator wakes up on the flip")
	_, err = svc.SetChannelMonitorMode(context.Background(), "v4")
	require.ErrorIs(t, err, ErrChannelMonitorInvalidMode)
	require.Equal(t, ChannelMonitorModeV3, normalizeChannelMonitorMode("v3"))

	v2 := ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV2}
	v3 := ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV3}
	require.True(t, v2.PassiveAggregationAllowed())
	require.True(t, v3.PassiveAggregationAllowed(), "V3 is built on the passive facts")
	require.True(t, v2.V2Active())
	require.False(t, v3.V2Active(), "V2 views and candy probes stay off in v3")
	require.True(t, v3.V3Active())
	require.False(t, v3.ActiveProbesAllowed())
	require.False(t, ChannelMonitorRuntime{Enabled: false, Mode: ChannelMonitorModeV3}.PassiveAggregationAllowed())
}

type settingRepoForMonitorMode struct {
	SettingRepository
	values map[string]string
}

func (r *settingRepoForMonitorMode) Set(_ context.Context, key, value string) error {
	if key == "" {
		return errors.New("empty key")
	}
	r.values[key] = value
	return nil
}
