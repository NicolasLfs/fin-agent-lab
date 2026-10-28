#!/bin/sh
set -eu

: "${PGHOST:?PGHOST is required}"
: "${PGDATABASE:?PGDATABASE is required}"
: "${PGUSER:?PGUSER is required}"
: "${PGPASSWORD:?PGPASSWORD is required}"

MIGRATIONS_DIR=${MIGRATIONS_DIR:-/migrations}
attempt=0
until pg_isready -h "$PGHOST" -U "$PGUSER" -d "$PGDATABASE"; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 60 ]; then
    echo "PostgreSQL did not become ready for migrations." >&2
    exit 1
  fi
  sleep 2
done

psql -v ON_ERROR_STOP=1 -c \
  'CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())'

temp_sql=$(mktemp)
trap 'rm -f "$temp_sql"' EXIT HUP INT TERM

for migration in "$MIGRATIONS_DIR"/*.up.sql; do
  [ -f "$migration" ] || continue
  filename=${migration##*/}
  version=${filename%%_*}
  applied=$(psql -v ON_ERROR_STOP=1 -Atqc \
    "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $version)")
  [ "$applied" = "t" ] && continue

  {
    printf 'BEGIN;\n'
    cat "$migration"
    printf '\nINSERT INTO schema_migrations (version) VALUES (%s);\nCOMMIT;\n' "$version"
  } > "$temp_sql"
  psql -v ON_ERROR_STOP=1 -f "$temp_sql"
  printf 'Applied migration %s.\n' "$version"
done
