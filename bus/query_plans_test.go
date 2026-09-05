package bus

import (
	"strings"
	"testing"
)

func TestActiveAdmissionQueriesAvoidHistoricalIndexes(t *testing.T) {
	a := setupAgents(t, ":memory:")
	defer a.runtime.Close()
	s := sqliteStore(t, a.runtime)
	for _, query := range []string{
		"SELECT COUNT(*) FROM messages WHERE scope_id='test' AND state NOT IN ('acknowledged','expired')",
		"SELECT COUNT(*) FROM tasks WHERE scope_id='test' AND status!='done'",
	} {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN " + query)
		requireNoError(t, err)
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			requireNoError(t, rows.Scan(&id, &parent, &unused, &detail))
			plan += detail
		}
		rows.Close()
		t.Log(plan)
		if !strings.Contains(plan, "_active") {
			t.Errorf("active admission scans historical index: %s", plan)
		}
	}
}
