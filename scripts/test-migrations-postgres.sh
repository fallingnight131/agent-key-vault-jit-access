#!/bin/sh
set -eu

postgres_data=$(mktemp -d /tmp/akv-postgres.XXXXXX)
postgres_socket=$(mktemp -d /tmp/akv-postgres-socket.XXXXXX)

cleanup() {
	pg_ctl -D "$postgres_data" -m immediate stop >/dev/null 2>&1 || true
	rm -rf "$postgres_data" "$postgres_socket"
}
trap cleanup EXIT HUP INT TERM

initdb -D "$postgres_data" -A trust -U akvtest --no-locale >/dev/null
pg_ctl -D "$postgres_data" -o "-F -k $postgres_socket -h ''" -w start >/dev/null
createdb -h "$postgres_socket" -U akvtest akvtest
for migration in internal/store/migrations/*.sql; do
	if [ "$(basename "$migration")" = "006_safe_operations.sql" ]; then
		psql -X -v ON_ERROR_STOP=1 -h "$postgres_socket" -U akvtest -d akvtest >/dev/null <<'SQL'
INSERT INTO users (id,username,password_hash,status)
VALUES ('00000000-0000-4000-8000-000000000101','legacy-owner','fixture','ACTIVE');
INSERT INTO agents (id,owner_user_id,name,status)
VALUES ('00000000-0000-4000-8000-000000000102','00000000-0000-4000-8000-000000000101','legacy-agent','ACTIVE');
INSERT INTO tasks (id,agent_id,status,created_at,last_heartbeat_at)
VALUES ('00000000-0000-7000-8000-000000000103','00000000-0000-4000-8000-000000000102','ACTIVE',now(),now());
INSERT INTO targets (id,name,connector_type,connection_config,status)
VALUES ('00000000-0000-4000-8000-000000000104','legacy-target','HTTP','{"base_url":"https://legacy.invalid","allowed_http_methods":["GET"]}','ACTIVE');
INSERT INTO credentials (id,target_id,alias,credential_type,status,vault_provider,vault_path)
VALUES ('00000000-0000-4000-8000-000000000105','00000000-0000-4000-8000-000000000104','default','API_KEY','ACTIVE','OPENBAO','legacy/reference');
UPDATE targets SET default_credential_id='00000000-0000-4000-8000-000000000105'
WHERE id='00000000-0000-4000-8000-000000000104';
INSERT INTO authorization_requests
    (id,agent_id,task_id,target_id,credential_id,operation,parameters,operation_hash,reason,status,created_at,approval_deadline)
VALUES
    ('00000000-0000-4000-8000-000000000106','00000000-0000-4000-8000-000000000102','00000000-0000-7000-8000-000000000103','00000000-0000-4000-8000-000000000104','00000000-0000-4000-8000-000000000105','HTTP','{}',decode(repeat('01',32),'hex'),'legacy pending','PENDING_APPROVAL',now(),now()+interval '30 minutes'),
    ('00000000-0000-4000-8000-000000000107','00000000-0000-4000-8000-000000000102','00000000-0000-7000-8000-000000000103','00000000-0000-4000-8000-000000000104','00000000-0000-4000-8000-000000000105','HTTP','{}',decode(repeat('02',32),'hex'),'legacy approved','APPROVED',now(),now()+interval '30 minutes'),
    ('00000000-0000-4000-8000-000000000108','00000000-0000-4000-8000-000000000102','00000000-0000-7000-8000-000000000103','00000000-0000-4000-8000-000000000104','00000000-0000-4000-8000-000000000105','HTTP','{}',decode(repeat('03',32),'hex'),'legacy executing','APPROVED',now(),now()+interval '30 minutes');
INSERT INTO operation_grants
    (id,request_id,agent_id,task_id,target_id,credential_id,operation_hash,approved_at,expires_at,status,claimed_at)
VALUES
    ('00000000-0000-4000-8000-000000000109','00000000-0000-4000-8000-000000000107','00000000-0000-4000-8000-000000000102','00000000-0000-7000-8000-000000000103','00000000-0000-4000-8000-000000000104','00000000-0000-4000-8000-000000000105',decode(repeat('02',32),'hex'),now(),now()+interval '10 minutes','APPROVED',NULL),
    ('00000000-0000-4000-8000-000000000110','00000000-0000-4000-8000-000000000108','00000000-0000-4000-8000-000000000102','00000000-0000-7000-8000-000000000103','00000000-0000-4000-8000-000000000104','00000000-0000-4000-8000-000000000105',decode(repeat('03',32),'hex'),now(),now()+interval '10 minutes','EXECUTING',now());
SQL
	fi
	psql -X -v ON_ERROR_STOP=1 -h "$postgres_socket" -U akvtest -d akvtest -f "$migration" >/dev/null
done

legacy_pending_status=$(psql -X -At -h "$postgres_socket" -U akvtest -d akvtest \
	-c "SELECT status FROM authorization_requests WHERE id='00000000-0000-4000-8000-000000000106'")
legacy_approved_status=$(psql -X -At -h "$postgres_socket" -U akvtest -d akvtest \
	-c "SELECT status FROM operation_grants WHERE id='00000000-0000-4000-8000-000000000109'")
legacy_executing_cancelled=$(psql -X -At -h "$postgres_socket" -U akvtest -d akvtest \
	-c "SELECT revoked_at IS NOT NULL FROM operation_grants WHERE id='00000000-0000-4000-8000-000000000110'")

test "$legacy_pending_status" = "APPROVAL_EXPIRED"
test "$legacy_approved_status" = "REVOKED"
test "$legacy_executing_cancelled" = "t"

if psql -X -v ON_ERROR_STOP=1 -h "$postgres_socket" -U akvtest -d akvtest >/dev/null 2>&1 \
	-c "INSERT INTO authorization_requests (id,agent_id,task_id,target_id,credential_id,operation,parameters,operation_hash,reason,status,created_at,approval_deadline) VALUES ('00000000-0000-4000-8000-000000000111','00000000-0000-4000-8000-000000000102','00000000-0000-7000-8000-000000000103','00000000-0000-4000-8000-000000000104','00000000-0000-4000-8000-000000000105','HTTP','{}',decode(repeat('04',32),'hex'),'legacy insert','PENDING_APPROVAL',now(),now()+interval '30 minutes')"; then
	exit 1
fi

psql -X -v ON_ERROR_STOP=1 -h "$postgres_socket" -U akvtest -d akvtest \
	-c "TRUNCATE targets,users CASCADE" >/dev/null 2>&1

table_count=$(psql -X -At -h "$postgres_socket" -U akvtest -d akvtest \
	-c "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'")
trigger_count=$(psql -X -At -h "$postgres_socket" -U akvtest -d akvtest \
	-c "SELECT count(*) FROM pg_trigger WHERE tgname = 'authorization_requests_immutable_snapshot'")

test "$table_count" -eq 18
test "$trigger_count" -eq 1

AKV_TEST_POSTGRES_DSN="host=$postgres_socket user=akvtest dbname=akvtest sslmode=disable" \
	GOCACHE="${GOCACHE:-/tmp/akv-go-cache}" \
	go test -race ./internal/store -run TestPostgreSQL -count=1

AKV_TEST_POSTGRES_DSN="host=$postgres_socket user=akvtest dbname=akvtest sslmode=disable" \
	AKV_TEST_POSTGRES_SOCKET="$postgres_socket" \
	GOCACHE="${GOCACHE:-/tmp/akv-go-cache}" \
	go test -race ./internal/proxy -run 'Test(PostgreSQLEndToEndAuthorizationFlow|PGXFactoryConnectsToTemporaryPostgreSQL)' -count=1

AKV_TEST_POSTGRES_DSN="host=$postgres_socket user=akvtest dbname=akvtest sslmode=disable" \
	AKV_TEST_POSTGRES_SOCKET="$postgres_socket" \
	GOCACHE="${GOCACHE:-/tmp/akv-go-cache}" \
	go test -race ./internal/behavior -count=1
