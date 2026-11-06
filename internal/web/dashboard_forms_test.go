package web

import (
	"strings"
	"testing"
)

// formBlock returns the dashboard HTML from heading to the first
// closing </form> after it: the one form that heading introduces.
func formBlock(t *testing.T, body, heading string) string {
	t.Helper()
	i := strings.Index(body, heading)
	if i < 0 {
		t.Fatalf("dashboard has no %q heading", heading)
	}
	j := strings.Index(body[i:], "</form>")
	if j < 0 {
		t.Fatalf("no </form> after %q", heading)
	}
	return body[i : i+j]
}

// Each hand-entry card must keep its own inputs inside its own form. A
// bad template edit once moved the RSU form's symbol/date/quantity
// inputs out of it, and nothing noticed because the other tests only
// look for fragments somewhere on the page.
func TestHandler_Dashboard_EachEntryFormKeepsItsOwnInputs(t *testing.T) {
	conn := openMigratedTestDB(t)
	body := dashboardBody(t, conn, "/")

	cases := []struct {
		heading string
		action  string
		fields  []string
	}{
		{"<h2>Log RSU vest</h2>", `action="/rsu"`, []string{"symbol", "date", "quantity", "fmv", "currency"}},
		{"<h2>Log stock sale</h2>", `action="/sale"`, []string{"symbol", "date", "quantity", "price", "currency"}},
		{"<h2>Log trade</h2>", `action="/trade"`, []string{"kind", "instrument", "new_instrument", "classification", "date", "quantity", "price", "currency"}},
	}
	for _, tc := range cases {
		block := formBlock(t, body, tc.heading)
		if !strings.Contains(block, tc.action) {
			t.Errorf("%s: form does not post to %s", tc.heading, tc.action)
		}
		for _, f := range tc.fields {
			if !strings.Contains(block, `name="`+f+`"`) {
				t.Errorf("%s: its form is missing the %q input", tc.heading, f)
			}
		}
	}
}
