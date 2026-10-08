package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var controlledCanonicalSQL = map[string]string{
	"screen-coding-01":  "SELECT c.customer_id, COALESCE(SUM(o.amount_cents-COALESCE(r.refund,0)),0) FROM customers c LEFT JOIN orders o ON o.customer_id=c.customer_id AND o.status='paid' LEFT JOIN (SELECT order_id,SUM(amount_cents) refund FROM refunds GROUP BY order_id) r ON r.order_id=o.order_id WHERE c.active=1 GROUP BY c.customer_id ORDER BY c.customer_id",
	"screen-coding-02":  "SELECT entity_id,revision,value FROM events e WHERE NOT EXISTS (SELECT 1 FROM events n WHERE n.entity_id=e.entity_id AND (n.revision>e.revision OR (n.revision=e.revision AND n.seq>e.seq))) AND status<>'deleted' ORDER BY entity_id",
	"screen-coding-03":  "SELECT DISTINCT s.person FROM skills s WHERE NOT EXISTS (SELECT 1 FROM requirements r WHERE NOT EXISTS (SELECT 1 FROM skills t WHERE t.person=s.person AND t.skill=r.skill)) ORDER BY s.person",
	"confirm-coding-01": "WITH days AS (SELECT DISTINCT customer_id,day FROM orders WHERE status='paid'), marked AS (SELECT customer_id,day,day-ROW_NUMBER() OVER(PARTITION BY customer_id ORDER BY day) g FROM days) SELECT customer_id,MIN(day),MAX(day),COUNT(*) FROM marked GROUP BY customer_id,g HAVING COUNT(*)>=3 ORDER BY customer_id,MIN(day)",
	"confirm-coding-02": "WITH running AS (SELECT o.order_id,o.amount_cents,r.refund_id,SUM(r.amount_cents) OVER(PARTITION BY o.order_id ORDER BY r.refund_id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) cumulative FROM orders o JOIN refunds r ON r.order_id=o.order_id WHERE o.status='paid'), hits AS (SELECT *,ROW_NUMBER() OVER(PARTITION BY order_id ORDER BY refund_id) n FROM running WHERE cumulative>=amount_cents) SELECT order_id,refund_id,cumulative FROM hits WHERE n=1 ORDER BY order_id",
	"confirm-coding-03": "SELECT a.interval_id,a.start_at,a.end_at FROM intervals a WHERE NOT EXISTS (SELECT 1 FROM intervals b WHERE b.start_at<=a.start_at AND b.end_at>=a.end_at AND (b.start_at<a.start_at OR b.end_at>a.end_at)) ORDER BY a.start_at,a.end_at,a.interval_id",
	"confirm-coding-04": "SELECT p.person,r.skill FROM (SELECT DISTINCT person FROM skills) p CROSS JOIN requirements r WHERE NOT EXISTS(SELECT 1 FROM skills s WHERE s.person=p.person AND s.skill=r.skill) ORDER BY p.person,r.skill",
	"confirm-coding-05": "WITH t AS (SELECT c.region,COALESCE(SUM(CASE WHEN o.status='paid' THEN o.amount_cents ELSE 0 END),0) p,COALESCE(SUM(o.amount_cents),0) a FROM customers c LEFT JOIN orders o ON o.customer_id=c.customer_id GROUP BY c.region) SELECT region,p,a,CASE WHEN a=0 THEN 0 ELSE 10000*p/a END FROM t ORDER BY region",
	"confirm-coding-06": "WITH w AS (SELECT sensor_id,day,SUM(value) OVER(PARTITION BY sensor_id ORDER BY day ROWS BETWEEN 2 PRECEDING AND CURRENT ROW) s,COUNT(*) OVER(PARTITION BY sensor_id ORDER BY day ROWS BETWEEN 2 PRECEDING AND CURRENT ROW) n FROM readings WHERE value IS NOT NULL) SELECT sensor_id,day,s FROM w WHERE n=3 ORDER BY sensor_id,day",
	"confirm-coding-07": "SELECT c.customer_id,(SELECT MAX(o.amount_cents) FROM orders o WHERE o.customer_id=c.customer_id AND o.status='paid' AND o.amount_cents<(SELECT MAX(p.amount_cents) FROM orders p WHERE p.customer_id=c.customer_id AND p.status='paid')) FROM customers c ORDER BY c.customer_id",
	"confirm-coding-08": "SELECT a.interval_id,COUNT(b.interval_id) FROM intervals a LEFT JOIN intervals b ON b.interval_id<>a.interval_id AND a.start_at<b.end_at AND b.start_at<a.end_at GROUP BY a.interval_id ORDER BY a.interval_id",
}

func TestControlledExperimentFrozenSuiteAndGraders(t *testing.T) {
	tasks, err := ControlledExperimentTasks()
	require.NoError(t, err)
	require.Len(t, tasks, 44)
	counts := map[string]int{}
	families := map[string]string{}
	for _, task := range tasks {
		t.Run(task.ID, func(t *testing.T) {
			counts[task.Split+"/"+task.Category]++
			if previous, ok := families[task.Family]; ok {
				require.Equal(t, previous, task.Split)
			}
			families[task.Family] = task.Split
			answer := string(task.Grader.Expected)
			trace := make([]ControlledToolTrace, 0)
			if task.Grader.Kind == "sql" {
				require.NoError(t, validateControlledSQL(controlledCanonicalSQL[task.ID]))
				raw, err := json.Marshal(map[string]string{"query": controlledCanonicalSQL[task.ID]})
				require.NoError(t, err)
				answer = string(raw)
			}
			if task.Grader.Kind == "tool_json" {
				for _, id := range task.Grader.RequiredRecordIDs {
					call, _ := json.Marshal(map[string]any{"type": "function_call", "name": "read_record", "namespace": "fixture", "call_id": id, "arguments": fmt.Sprintf("{\"record_id\":%q}", id)})
					result, entry, err := executeControlledTool(task, call)
					require.NoError(t, err)
					require.True(t, entry.OK)
					require.Contains(t, string(result), "function_call_output")
					trace = append(trace, entry)
				}
				require.False(t, GradeControlledTask(context.Background(), task, answer, nil).Passed)
			}
			grade := GradeControlledTask(context.Background(), task, answer, trace)
			require.True(t, grade.Passed, "%+v", grade)
			require.Equal(t, 1.0, grade.Score)
			require.False(t, GradeControlledTask(context.Background(), task, "{}", trace).Passed)
			payload, err := controlledPayload(ControlledExperimentSpec{Model: "gpt-6.1-sol", ReasoningEffort: "high"}, task, nil)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(payload, &fields))
			require.NotContains(t, fields, "grader")
			require.NotContains(t, fields, "expected")
			require.NotContains(t, fields, "records")
		})
	}
	for _, category := range []string{"coding", "constraints", "long_context", "tools"} {
		require.Equal(t, 3, counts["screen/"+category])
		require.Equal(t, 8, counts["confirm/"+category])
	}
}

func TestControlledExperimentJSONContracts(t *testing.T) {
	for _, raw := range []string{`{"n":1,"n":2}`, `{"x":true} {}`, `{"n":NaN}`, strings.Repeat("[", 42) + "0" + strings.Repeat("]", 42)} {
		_, err := decodeControlledJSON([]byte(raw))
		require.Error(t, err, raw)
	}
	task := ControlledTask{Grader: ControlledGrader{Kind: "exact_json", Expected: json.RawMessage(`{"n":1}`)}}
	for _, answer := range []string{`{"n":true}`, `{"n":1.0}`, `{"n":1,"extra":2}`, "\x60\x60\x60json\n{\"n\":1}\n\x60\x60\x60"} {
		require.False(t, GradeControlledTask(context.Background(), task, answer, nil).Passed)
	}
}

func TestControlledExperimentSQLRejectsIOWritesAndUnboundedQueries(t *testing.T) {
	for _, query := range []string{"DELETE FROM events", "SELECT 1; ATTACH ':memory:' AS other", "SELECT load_extension('x')", "SELECT readfile('x')", "SELECT * FROM sqlite_master", "SELECT * FROM pragma_table_info('events')", "WITH RECURSIVE a(x) AS (SELECT 1) SELECT * FROM a", "SELECT randomblob(1000000)", "SELECT ?"} {
		require.Error(t, validateControlledSQL(query), query)
	}
	require.NoError(t, validateControlledSQL("SELECT substr('attach; sqlite_master',1,2) /* harmless */"))
	rows, err := controlledSQLRows(context.Background(), map[string][][]any{"events": {}}, "SELECT value FROM events")
	require.NoError(t, err)
	require.Equal(t, []any{}, rows)
	rows, err = controlledSQLRows(context.Background(), nil, "WITH v(a,b) AS (SELECT 1,2), w(x) AS NOT MATERIALIZED (SELECT a+b FROM v) SELECT x FROM w")
	require.NoError(t, err)
	require.Equal(t, []any{[]any{json.Number("3")}}, rows)
	require.Error(t, validateControlledSQL("WITH load_extension(x) AS (SELECT 1) SELECT load_extension(x) FROM load_extension"))
}

func TestControlledExperimentSingleSubmissionAndChannelGate(t *testing.T) {
	mode := &controlledExperimentMode{channel: "bps"}
	ctx := context.WithValue(context.Background(), controlledExperimentContextKey{}, mode)
	require.Error(t, controlledSubmission(ctx, "native_http"))
	require.Equal(t, int32(0), mode.submissions.Load())
	require.NoError(t, controlledSubmission(ctx, "bps"))
	require.Error(t, controlledSubmission(ctx, "bps"))
	require.Equal(t, int32(1), mode.submissions.Load())
	require.True(t, isQualityObservation(ctx))
	require.NoError(t, controlledSubmission(context.Background(), "native_http"))
}

func controlledTestSSE(events ...any) []byte {
	var out strings.Builder
	for _, event := range events {
		data, _ := json.Marshal(event)
		_, _ = out.WriteString("data: ")
		_, _ = out.Write(data)
		_, _ = out.WriteString("\n\n")
	}
	return []byte(out.String())
}

func TestControlledExperimentProtocolEvidence(t *testing.T) {
	message := map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "{\"n\":1}"}}}
	terminal := map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_1", "status": "completed", "model": "gpt-6.1-sol", "reasoning": map[string]any{"effort": "high"}, "output": []any{message}}}
	item := map[string]any{"type": "response.output_item.done", "output_index": 0, "item": message}
	turn, model, effort, event, err := parseControlledResponse(controlledTestSSE(item, terminal))
	require.NoError(t, err)
	require.Equal(t, `{"n":1}`, turn.Text)
	require.Equal(t, "gpt-6.1-sol", model)
	require.Equal(t, "high", effort)
	require.Equal(t, "response.completed", event)
	for _, wire := range [][]byte{
		controlledTestSSE(item), controlledTestSSE(terminal, terminal), controlledTestSSE(terminal, item),
		[]byte("data: {oops}\n\n"), controlledTestSSE(map[string]any{"type": "response.failed"}),
		controlledTestSSE(map[string]any{"type": "response.created", "response": map[string]any{"id": "other"}}, terminal),
	} {
		_, _, _, _, err := parseControlledResponse(wire)
		require.Error(t, err)
	}
	noOutput := map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_1", "status": "completed", "model": "gpt-6.1-sol", "output": []any{}}}
	turn, _, _, _, err = parseControlledResponse(controlledTestSSE(item, noOutput))
	require.NoError(t, err)
	require.Len(t, turn.Output, 1)
	conflicting := map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "role": "assistant", "content": []any{}}}
	_, _, _, _, err = parseControlledResponse(controlledTestSSE(conflicting, terminal))
	require.Error(t, err)
}

func TestControlledExperimentReportSeparatesQualityAndProtocol(t *testing.T) {
	run := &ControlledExperiment{Spec: ControlledExperimentSpec{Routes: []ControlledRoute{{AccountID: 1}, {AccountID: 2}}, Tasks: []ControlledTask{{ID: "a"}, {ID: "b"}, {ID: "c"}}, Repetitions: 1}}
	good := ControlledDiagnostic{IdentityStable: true, ResponseModel: "same", EffectiveEffort: "high", EffortEvidence: "upstream_declared"}
	attempts := []*ControlledAttempt{
		{RouteIndex: 0, Phase: "task", TaskID: "a", Repetition: 1, Status: "completed", Grade: &ControlledGrade{Passed: true, Score: 1}, Diagnostic: good},
		{RouteIndex: 1, Phase: "task", TaskID: "a", Repetition: 1, Status: "completed", Grade: &ControlledGrade{Score: 0}, Diagnostic: good},
		{RouteIndex: 0, Phase: "task", TaskID: "b", Repetition: 1, Status: "unknown"},
		{RouteIndex: 1, Phase: "task", TaskID: "b", Repetition: 1, Status: "completed", Grade: &ControlledGrade{Passed: true, Score: 1}, Diagnostic: good},
		{RouteIndex: 0, Phase: "task", TaskID: "c", Repetition: 1, Status: "completed", Grade: &ControlledGrade{Passed: true, Score: 1}, Diagnostic: good},
		{RouteIndex: 1, Phase: "task", TaskID: "c", Repetition: 1, Status: "completed", Grade: &ControlledGrade{Passed: true, Score: 1}, Diagnostic: ControlledDiagnostic{IdentityStable: true, ResponseModel: "different", EffectiveEffort: "high", EffortEvidence: "upstream_declared"}},
	}
	report := buildControlledReport(run, attempts, nil)
	require.Equal(t, 1, report.Routes[0].UnknownCalls)
	require.Equal(t, 2, report.Routes[0].GradedTasks)
	require.Equal(t, 1, report.Comparisons[0].PairedTasks)
	require.Equal(t, 2, report.Comparisons[0].ExcludedTasks)
	require.Equal(t, -1.0, *report.Comparisons[0].MeanDifference)
	attempts[1].Diagnostic.EffortEvidence = "request_sent"
	require.False(t, controlledComparable(attempts[0], attempts[1]))
	attempts[1].Diagnostic.EffortEvidence = "adapter_declared"
	require.False(t, controlledComparable(attempts[0], attempts[1]))
}
