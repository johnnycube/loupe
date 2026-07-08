package postgres

import "embed"

// Migrations holds the goose migration files for the PostgreSQL backend, applied
// at startup by the repo layer. Versioned migrations (not a single re-run DDL
// blob) so schema changes are tracked and existing databases upgrade in place.
//
//go:embed migrations/*.sql
var Migrations embed.FS
