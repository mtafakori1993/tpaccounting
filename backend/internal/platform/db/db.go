// Package db provides the PostgreSQL pool and transaction helpers.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is satisfied by both *pgxpool.Pool and pgx.Tx so data-access code
// can run inside or outside a transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 20
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}
	var pool *pgxpool.Pool
	// The database container may still be starting; retry for a while.
	for attempt := 0; attempt < 30; attempt++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("connect to database: %w", err)
}

// WithTx runs fn in a transaction, committing on success and rolling back on error or panic.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsNoRows reports whether err means "no row found".
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// NextNumber atomically allocates the next document number in a scope.
func NextNumber(ctx context.Context, q DBTX, companyID int64, scope string, fiscalYearID int64) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `
		INSERT INTO doc_sequences (company_id, scope, fiscal_year_id, next_number)
		VALUES ($1, $2, $3, 2)
		ON CONFLICT (company_id, scope, fiscal_year_id)
		DO UPDATE SET next_number = doc_sequences.next_number + 1
		RETURNING next_number - 1`, companyID, scope, fiscalYearID).Scan(&n)
	return n, err
}

// BumpNumber makes sure the sequence continues after a manually chosen number.
func BumpNumber(ctx context.Context, q DBTX, companyID int64, scope string, fiscalYearID, used int64) error {
	_, err := q.Exec(ctx, `
		INSERT INTO doc_sequences (company_id, scope, fiscal_year_id, next_number)
		VALUES ($1, $2, $3, $4 + 1)
		ON CONFLICT (company_id, scope, fiscal_year_id)
		DO UPDATE SET next_number = GREATEST(doc_sequences.next_number, $4 + 1)`, companyID, scope, fiscalYearID, used)
	return err
}
