// Package schema 定义 Ent ORM 的数据库 schema。
package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AccountProxyLane describes one independently schedulable egress for an
// account.  It intentionally lives in its own table rather than extending
// accounts.proxy_id to a JSON array: each lane has its own concurrency,
// weight, health/cooldown state, and connection-pool identity.
type AccountProxyLane struct {
	ent.Schema
}

func (AccountProxyLane) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "account_proxy_lanes"},
	}
}

func (AccountProxyLane) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (AccountProxyLane) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id"),
		field.Int64("proxy_id").Optional().Nillable(),
		field.String("name").MaxLen(100).NotEmpty(),
		field.Enum("transport").
			Values("proxy", "direct").
			Default("proxy"),
		field.Int("concurrency").Default(1),
		field.Int("weight").Default(1),
		field.Int("priority").Default(50),
		field.String("status").MaxLen(20).Default("active"),
		field.Bool("schedulable").Default(true),
		field.Time("cooldown_until").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (AccountProxyLane) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("account", Account.Type).
			Unique().
			Required().
			Field("account_id"),
		edge.To("proxy", Proxy.Type).
			Unique().
			Field("proxy_id"),
	}
}

func (AccountProxyLane) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "name").Unique(),
		index.Fields("account_id", "status", "schedulable"),
		index.Fields("account_id", "priority"),
		index.Fields("proxy_id"),
	}
}
