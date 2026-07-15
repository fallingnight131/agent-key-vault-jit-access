#!/bin/sh
set -eu

postgres_data=$(mktemp -d /tmp/akv-postgres.XXXXXX)
postgres_socket=$(mktemp -d /tmp/akv-postgres-socket.XXXXXX)

cleanup() {
	pg_ctl -D "$postgres_data" -m immediate stop >/dev/null 2>&1 || true
}
trap cleanup EXIT HUP INT TERM

initdb -D "$postgres_data" -A trust -U akvtest --no-locale >/dev/null
pg_ctl -D "$postgres_data" -o "-F -k $postgres_socket -h ''" -w start >/dev/null
createdb -h "$postgres_socket" -U akvtest akvtest
psql -X -v ON_ERROR_STOP=1 -h "$postgres_socket" -U akvtest -d akvtest \
	-f internal/store/migrations/001_initial.sql >/dev/null

table_count=$(psql -X -At -h "$postgres_socket" -U akvtest -d akvtest \
	-c "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'")
trigger_count=$(psql -X -At -h "$postgres_socket" -U akvtest -d akvtest \
	-c "SELECT count(*) FROM pg_trigger WHERE tgname = 'authorization_requests_immutable_snapshot'")

test "$table_count" -eq 14
test "$trigger_count" -eq 1
