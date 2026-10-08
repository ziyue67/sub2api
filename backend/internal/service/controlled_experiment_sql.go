package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"

	"modernc.org/sqlite"
)

var controlledSQLSchemas = map[string]string{
	"customers":    "customer_id INTEGER PRIMARY KEY, region TEXT NOT NULL, active INTEGER NOT NULL",
	"orders":       "order_id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL, day INTEGER NOT NULL, status TEXT NOT NULL, amount_cents INTEGER NOT NULL",
	"refunds":      "refund_id INTEGER PRIMARY KEY, order_id INTEGER NOT NULL, amount_cents INTEGER NOT NULL",
	"events":       "entity_id TEXT NOT NULL, revision INTEGER NOT NULL, seq INTEGER PRIMARY KEY, status TEXT NOT NULL, value INTEGER NOT NULL",
	"readings":     "sensor_id TEXT NOT NULL, day INTEGER NOT NULL, value INTEGER, PRIMARY KEY(sensor_id,day)",
	"requirements": "skill TEXT PRIMARY KEY",
	"skills":       "person TEXT NOT NULL, skill TEXT NOT NULL, PRIMARY KEY(person,skill)",
	"intervals":    "interval_id INTEGER PRIMARY KEY, start_at INTEGER NOT NULL, end_at INTEGER NOT NULL",
}

// A deliberately small SELECT grammar: statements and I/O functions outside
// the published task contract are rejected before SQLite sees the query.
// The database contains synthetic fixtures only and has no registered UDFs.
func validateControlledSQL(query string) error {
	if len(query) == 0 || len(query) > 12000 {
		return errors.New("SQL size limit")
	}
	var tokens []string
	for i := 0; i < len(query); {
		c := query[i]
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if i+1 < len(query) && query[i:i+2] == "--" {
			for i < len(query) && query[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(query) && query[i:i+2] == "/*" {
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				return errors.New("unclosed SQL comment")
			}
			i += end + 4
			continue
		}
		if c == '\'' {
			i++
			closed := false
			for i < len(query) {
				if query[i] == '\'' {
					i++
					if i < len(query) && query[i] == '\'' {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return errors.New("unclosed SQL string")
			}
			tokens = append(tokens, "#literal")
			continue
		}
		if c == '"' || c == '`' || c == '[' {
			close := c
			if c == '[' {
				close = ']'
			}
			i++
			start := i
			for i < len(query) && query[i] != close {
				i++
			}
			if i == len(query) {
				return errors.New("unclosed SQL identifier")
			}
			tokens = append(tokens, strings.ToLower(query[start:i]))
			i++
			continue
		}
		if c == ';' {
			if strings.TrimSpace(query[i+1:]) != "" {
				return errors.New("multiple SQL statements")
			}
			break
		}
		if c == '?' || c == '$' || c == ':' || c == '@' {
			return errors.New("SQL parameters prohibited")
		}
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' {
			start := i
			i++
			for i < len(query) && (query[i] >= 'a' && query[i] <= 'z' || query[i] >= 'A' && query[i] <= 'Z' || query[i] >= '0' && query[i] <= '9' || query[i] == '_') {
				i++
			}
			tokens = append(tokens, strings.ToLower(query[start:i]))
			continue
		}
		if c >= '0' && c <= '9' {
			for i < len(query) && (query[i] >= '0' && query[i] <= '9' || query[i] == '.') {
				i++
			}
			tokens = append(tokens, "#number")
			continue
		}
		if !strings.ContainsRune("(),.*+-/=<>!|%", rune(c)) {
			return errors.New("unsupported SQL token")
		}
		tokens = append(tokens, string(c))
		i++
	}
	if len(tokens) == 0 || tokens[0] != "select" && tokens[0] != "with" {
		return errors.New("SELECT required")
	}
	denied := strings.Fields("insert update delete replace create drop alter attach detach pragma vacuum reindex analyze recursive returning sqlite_master sqlite_schema sqlite_temp_master sqlite_temp_schema")
	functions := " abs avg coalesce count dense_rank first_value ifnull instr lag last_value lead length lower ltrim max min nth_value nullif rank round row_number rtrim substr substring sum total trim typeof upper replace cast "
	grammar := " select with as exists in over filter and or not where when then else on by from join having case distinct materialized "
	for i, token := range tokens {
		for _, word := range denied {
			if token == word && (token != "replace" || i+1 >= len(tokens) || tokens[i+1] != "(") {
				return errors.New("prohibited SQL keyword")
			}
		}
		if strings.HasPrefix(token, "sqlite_") {
			return errors.New("SQL metadata prohibited")
		}
		if i+1 < len(tokens) && tokens[i+1] == "(" && len(token) > 0 && (token[0] >= 'a' && token[0] <= 'z' || token[0] == '_') {
			if token != "#number" && !strings.Contains(functions, " "+token+" ") && !strings.Contains(grammar, " "+token+" ") && !controlledSQLCTEColumns(tokens, i) {
				return errors.New("SQL function outside allowlist")
			}
		}
	}
	return nil
}

// An explicit CTE column list is a declaration, not a function invocation.
// Exempt only identifier/comma lists followed by AS and a CTE body; calling
// that same name as a function elsewhere must still pass the allowlist.
func controlledSQLCTEColumns(tokens []string, index int) bool {
	if index == 0 || tokens[index-1] != "with" && tokens[index-1] != "," {
		return false
	}
	column := true
	for i := index + 2; i < len(tokens); i++ {
		token := tokens[i]
		if token == ")" {
			if column || i+2 >= len(tokens) || tokens[i+1] != "as" {
				return false
			}
			next := i + 2
			if tokens[next] == "not" {
				next++
			}
			if next < len(tokens) && tokens[next] == "materialized" {
				next++
			}
			return next < len(tokens) && tokens[next] == "("
		}
		if column {
			if len(token) == 0 || (token[0] < 'a' || token[0] > 'z') && token[0] != '_' {
				return false
			}
		} else if token != "," {
			return false
		}
		column = !column
	}
	return false
}

func controlledSQLRows(parent context.Context, tables map[string][][]any, query string) (any, error) {
	if err := validateControlledSQL(query); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		schema, ok := controlledSQLSchemas[name]
		if !ok {
			return nil, errors.New("unknown fixture table")
		}
		if _, err = conn.ExecContext(ctx, "CREATE TABLE "+name+" ("+schema+")"); err != nil {
			return nil, err
		}
		for _, row := range tables[name] {
			values := make([]any, len(row))
			for i, value := range row {
				values[i] = value
				if n, ok := value.(json.Number); ok {
					values[i], err = n.Int64()
					if err != nil {
						return nil, err
					}
				}
			}
			marks := strings.TrimRight(strings.Repeat("?,", len(row)), ",")
			if _, err = conn.ExecContext(ctx, "INSERT INTO "+name+" VALUES ("+marks+")", values...); err != nil {
				return nil, err
			}
		}
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA trusted_schema=OFF"); err != nil {
		return nil, err
	}
	// SQLite's runtime limits bound compiled expressions and allocations as well
	// as the context deadline, output row cap and response byte cap below.
	for id, limit := range map[int]int{0: 262144, 1: 12000, 2: 128, 3: 80, 4: 24, 5: 40000, 7: 0, 6: 32} {
		if _, err = sqlite.Limit(conn, id, limit); err != nil {
			return nil, err
		}
	}
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := make([][]any, 0)
	size := 0
	for rows.Next() {
		if len(result) >= 2000 {
			return nil, errors.New("SQL row limit")
		}
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		size += len(raw)
		if size > 2<<20 {
			return nil, errors.New("SQL result byte limit")
		}
		result = append(result, values)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return decodeControlledJSON(raw)
}
