#!/usr/bin/env bash
# End-to-end test of a dbstash image against a real database container:
#   1. seed the database, back it up with the image, restore it, compare rows;
#   2. `docker stop` a throttled backup: dbstash (PID 1) must exit before
#      Docker's SIGKILL and leave nothing on the remote.
#
#   scripts/e2e.sh <engine> <dbstash-image> <database-image>
set -euo pipefail

engine=$1 image=$2 db_image=$3
rows=5000
# Special characters check that credentials survive URI decoding and the
# option files dbstash writes for the dump tools.
pw='p@ss w#rd' pw_uri='p%40ss%20w%23rd'
id=$$
net=dbstash-e2e-$id db=e2e-db-$id stopper=e2e-stop-$id restored=e2e-restored-$id
backups=$(mktemp -d) stopped=$(mktemp -d)
chmod 777 "$backups" "$stopped"

cleanup() {
	docker rm -f "$db" "$stopper" "$restored" >/dev/null 2>&1 || true
	docker network rm "$net" >/dev/null 2>&1 || true
	# Files written by containers are root-owned.
	docker run --rm -v "$backups:/a" -v "$stopped:/b" alpine:3.24 sh -c 'rm -rf /a/* /b/*' >/dev/null 2>&1 || true
	rm -rf "$backups" "$stopped"
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
retry() {
	for _ in $(seq 60); do
		"$@" >/dev/null 2>&1 && return 0
		sleep 2
	done
	fail "timed out waiting for: $*"
}
in_db() { docker exec "$db" "$@"; }

docker network create "$net" >/dev/null
rclone_conf=$(printf '[local]\ntype = local\n' | base64 -w0)

# dbstash <output dir> [docker run args...]
dbstash() {
	local dir=$1
	shift
	docker run --network "$net" -v "$dir:/backups" \
		-e DB_URI="$uri" -e RCLONE_CONFIG="$rclone_conf" -e RCLONE_REMOTE=local:/backups \
		-e BACKUP_SCHEDULE=once -e BACKUP_NAME_TEMPLATE='e2e-{timestamp}' "$@" "$image"
}

case $engine in
pg)
	docker run -d --name "$db" --network "$net" -e POSTGRES_PASSWORD="$pw" "$db_image" >/dev/null
	retry in_db pg_isready -U postgres -h 127.0.0.1
	in_db psql -U postgres -qc 'CREATE DATABASE e2e'
	in_db psql -U postgres -d e2e -qc "CREATE TABLE items AS SELECT generate_series(1, $rows) AS i"
	uri="postgresql://postgres:$pw_uri@$db:5432/e2e"
	compress=true
	restore() {
		in_db psql -U postgres -qc 'CREATE DATABASE restored'
		docker run --rm --network "$net" -v "$backups:/backups" -e PGPASSWORD="$pw" \
			--entrypoint pg_restore "$image" -h "$db" -U postgres -d restored "/backups/$1"
		in_db psql -U postgres -d restored -tAc 'SELECT count(*) FROM items'
	}
	;;
mongo)
	docker run -d --name "$db" --network "$net" \
		-e MONGO_INITDB_ROOT_USERNAME=root -e MONGO_INITDB_ROOT_PASSWORD="$pw" "$db_image" >/dev/null
	# The entrypoint restarts mongod after creating the user.
	retry sh -c "docker logs $db 2>&1 | grep -q 'init process complete'"
	mongosh() { in_db mongosh --quiet -u root -p "$pw" --authenticationDatabase admin "$@"; }
	retry mongosh --eval 'db.runCommand({ ping: 1 })'
	mongosh e2e --eval "db.items.insertMany(Array.from({ length: $rows }, (_, i) => ({ i })))" >/dev/null
	uri="mongodb://root:$pw_uri@$db:27017/e2e?authSource=admin"
	compress=true
	restore() {
		docker run --rm --network "$net" -v "$backups:/backups" --entrypoint mongorestore "$image" \
			--quiet --uri "mongodb://root:$pw_uri@$db:27017/?authSource=admin" --archive="/backups/$1" --gzip --nsFrom 'e2e.*' --nsTo 'restored.*'
		mongosh restored --eval 'db.items.countDocuments()'
	}
	;;
mysql | mariadb)
	client=$engine
	docker run -d --name "$db" --network "$net" -e MYSQL_ROOT_PASSWORD="$pw" -e MYSQL_DATABASE=e2e "$db_image" >/dev/null
	# TCP only answers once the real server is up, not the init-time one.
	retry in_db "$client" -h 127.0.0.1 -uroot -p"$pw" -e 'SELECT 1'
	in_db "$client" -h 127.0.0.1 -uroot -p"$pw" e2e \
		-e "CREATE TABLE items (i INT); INSERT INTO items VALUES ($(seq -s '),(' 1 "$rows"));"
	uri="mysql://root:$pw_uri@$db:3306/e2e"
	compress=false
	restore() {
		in_db "$client" -h 127.0.0.1 -uroot -p"$pw" -e 'CREATE DATABASE restored'
		docker exec -i "$db" "$client" -h 127.0.0.1 -uroot -p"$pw" restored <"$backups/$1"
		in_db "$client" -h 127.0.0.1 -uroot -p"$pw" -N -e 'SELECT COUNT(*) FROM restored.items'
	}
	;;
redis)
	docker run -d --name "$db" --network "$net" "$db_image" redis-server --requirepass "$pw" >/dev/null
	retry docker exec -e REDISCLI_AUTH="$pw" "$db" redis-cli ping
	seq 1 "$rows" | awk '{ print "SET key:" $1 " " $1 }' | docker exec -i -e REDISCLI_AUTH="$pw" "$db" redis-cli >/dev/null
	uri="redis://:$pw_uri@$db:6379"
	compress=false
	restore() {
		docker run -d --name "$restored" -v "$backups/$1:/data/dump.rdb:ro" "$db_image" >/dev/null
		retry docker exec "$restored" redis-cli ping
		docker exec "$restored" redis-cli dbsize
	}
	;;
*)
	fail "unknown engine: $engine"
	;;
esac

echo "--- $engine: backup"
dbstash "$backups" --rm -e BACKUP_COMPRESS="$compress"
files=("$backups"/*)
[ "${#files[@]}" -eq 1 ] && [ -s "${files[0]}" ] || fail "expected one non-empty backup, got: $(ls -A "$backups")"
file=$(basename "${files[0]}")
echo "backup: $file ($(wc -c <"$backups/$file") bytes)"

echo "--- $engine: restore"
count=$(restore "$file" | tr -d '[:space:]')
[ "$count" = "$rows" ] || fail "restored $count rows, want $rows"
echo "restored $count rows"

echo "--- $engine: docker stop during upload"
dbstash "$stopped" -d --name "$stopper" -e BACKUP_COMPRESS="$compress" -e RCLONE_EXTRA_ARGS='--bwlimit 1k' >/dev/null
sleep 3
[ "$(docker inspect -f '{{.State.Running}}' "$stopper")" = true ] || fail "backup finished before the stop; raise the row count"
start=$(date +%s)
docker stop "$stopper" >/dev/null # SIGTERM, then SIGKILL after 10s
took=$(($(date +%s) - start))
code=$(docker inspect -f '{{.State.ExitCode}}' "$stopper")
echo "stopped in ${took}s, exit code $code"
[ "$code" != 137 ] && [ "$took" -lt 10 ] || fail "dbstash did not exit on SIGTERM (needed SIGKILL)"
[ -z "$(ls -A "$stopped")" ] || fail "partial upload left behind: $(ls -A "$stopped")"

echo "PASS: $engine"
