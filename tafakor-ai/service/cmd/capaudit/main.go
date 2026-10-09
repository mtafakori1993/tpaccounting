// Command capaudit prints the execution capability registry (D59) — every operation
// with its planner exposure, direct/UI executors, verification, approval and
// idempotency — as JSON (default) or a Markdown table (-md). The CI invariant itself is
// TestCapabilityInvariantPlannerImpliesExecutor; this is the readable report.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/tafakorpardazan/tafakor-ai/internal/agent"
)

func yn(b bool) string {
	if b {
		return "YES"
	}
	return "NO"
}

func main() {
	md := flag.Bool("md", false, "markdown table")
	flag.Parse()
	rows := agent.CapabilityMatrix()
	sort.Slice(rows, func(i, j int) bool { return rows[i].Tool < rows[j].Tool })
	if !*md {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", " ")
		_ = enc.Encode(rows)
		return
	}
	fmt.Println("| Operation | Kind | Planner | Direct | UI | Verify | Approval | Destructive | Idempotent | Blocked / UI note |")
	fmt.Println("|---|---|:-:|:-:|:-:|---|---|:-:|---|---|")
	for _, r := range rows {
		note := r.Blocked
		if note == "" && !r.UI && r.Kind != "read" && r.Kind != "export" && r.Kind != "compute" {
			note = "UI: " + r.UIReason
		}
		fmt.Printf("| `%s` | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", r.Tool, r.Kind, yn(r.Planner), yn(r.Direct), yn(r.UI), r.Verify, r.Approval, yn(r.Destructive), r.Idempotency, note)
	}
}
