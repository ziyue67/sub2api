package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imageArchiveRepository struct{ db *sql.DB }

func NewImageArchiveRepository(db *sql.DB) service.ImageArchiveRepository {
	return &imageArchiveRepository{db: db}
}

func (r *imageArchiveRepository) Lock(ctx context.Context) (func(), error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(719240300)`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return func() { _ = tx.Rollback() }, nil
}

func (r *imageArchiveRepository) Insert(ctx context.Context, v *service.ImageArchiveRecord) error {
	params, err := json.Marshal(v.Parameters)
	if err != nil {
		return err
	}
	files, err := json.Marshal(v.Files)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO image_request_archives
	(id,user_id,api_key_id,group_id,account_id,request_id,model,upstream_model,endpoint,prompt,parameters,files,status,error_codes,byte_size,created_at,expires_at)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12::jsonb,$13,$14,$15,$16,$17)`,
		v.ID, v.UserID, v.APIKeyID, v.GroupID, v.AccountID, v.RequestID, v.Model, v.UpstreamModel, v.Endpoint, v.Prompt, string(params), string(files), v.Status, v.ErrorCodes, v.ByteSize, v.CreatedAt, v.ExpiresAt)
	return err
}

const imageArchiveSelect = `SELECT a.id::text,a.user_id,a.api_key_id,a.group_id,a.account_id,a.request_id,a.model,a.upstream_model,a.endpoint,a.prompt,a.parameters,a.files,a.status,a.error_codes,a.byte_size,a.created_at,a.expires_at,
	COALESCE(u.email,''),COALESCE(k.name,''),COALESCE(g.name,''),COALESCE(ac.name,'')
	FROM image_request_archives a LEFT JOIN users u ON u.id=a.user_id LEFT JOIN api_keys k ON k.id=a.api_key_id
	LEFT JOIN groups g ON g.id=a.group_id LEFT JOIN accounts ac ON ac.id=a.account_id`

func scanImageArchive(row interface{ Scan(...any) error }) (service.ImageArchiveRecord, error) {
	var v service.ImageArchiveRecord
	var params, files []byte
	err := row.Scan(&v.ID, &v.UserID, &v.APIKeyID, &v.GroupID, &v.AccountID, &v.RequestID, &v.Model, &v.UpstreamModel, &v.Endpoint, &v.Prompt, &params, &files, &v.Status, &v.ErrorCodes, &v.ByteSize, &v.CreatedAt, &v.ExpiresAt, &v.UserEmail, &v.APIKeyName, &v.GroupName, &v.AccountName)
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(params, &v.Parameters); err != nil {
		return v, err
	}
	err = json.Unmarshal(files, &v.Files)
	return v, err
}

func (r *imageArchiveRepository) Get(ctx context.Context, id string) (*service.ImageArchiveRecord, error) {
	v, err := scanImageArchive(r.db.QueryRowContext(ctx, imageArchiveSelect+` WHERE a.id=$1`, id))
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func imageArchiveWhere(f service.ImageArchiveFilter) (string, []any) {
	clauses := []string{"a.expires_at > NOW()"}
	args := []any{}
	add := func(column string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf("%s $%d", column, len(args)))
	}
	if f.UserID > 0 {
		add("a.user_id =", f.UserID)
	}
	if f.AccountID > 0 {
		add("a.account_id =", f.AccountID)
	}
	if f.GroupID > 0 {
		add("a.group_id =", f.GroupID)
	}
	if f.Model != "" {
		add("a.model =", f.Model)
	}
	if f.From != nil {
		add("a.created_at >=", *f.From)
	}
	if f.To != nil {
		add("a.created_at <=", *f.To)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func (r *imageArchiveRepository) List(ctx context.Context, f service.ImageArchiveFilter) ([]service.ImageArchiveRecord, int64, error) {
	where, args := imageArchiveWhere(f)
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_request_archives a`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	pageSize := min(max(f.PageSize, 1), 100)
	page := max(f.Page, 1)
	args = append(args, pageSize, (page-1)*pageSize)
	query := imageArchiveSelect + where + fmt.Sprintf(` ORDER BY a.created_at DESC,a.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := []service.ImageArchiveRecord{}
	for rows.Next() {
		v, err := scanImageArchive(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, v)
	}
	return items, total, rows.Err()
}

func (r *imageArchiveRepository) Expired(ctx context.Context, before time.Time, limit int) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id::text FROM image_request_archives WHERE expires_at <= $1 ORDER BY expires_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *imageArchiveRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM image_request_archives WHERE id=$1`, id)
	return err
}
