#!/usr/bin/env bash
# Run the whole backend suite on every store (memory, sqlite, postgres) and
# print one summary line per store. See _docs/testing-guidelines.md.
#
#   scripts/test-matrix.sh                 all three stores
#   scripts/test-matrix.sh sqlite memory   only the named stores
#   scripts/test-matrix.sh --update-skips  rewrite backend/known_skips.txt from this run
#
# Postgres: TEST_DATABASE_URL, else the local :54329 throwaway server
# (_docs/AGENTS.md); if that server is not reachable the matrix FAILS. The run creates its own database (mg_matrix_<pid>), applies
# backend/db/schema.sql to it and drops it at the end, so parallel runs and the
# shared `mg` database are never touched. A store that cannot run FAILS the
# matrix; it is never skipped silently. A test skip that is not listed in
# backend/known_skips.txt for that store also fails the matrix.
# No `set -e`/pipefail: the script counts PASS/SKIP lines with grep (exit 1 on
# zero matches) and records go test's exit code itself; -e would abort the
# summary. Every step that can fail is checked explicitly instead.
set -u
root="$(cd "$(dirname "$0")/.." && pwd)"
. "$root/scripts/pgurl.sh"
known="$root/backend/known_skips.txt"
local_url="postgres://test@localhost:54329/mg?sslmode=disable"
update=0
stores=()
for a in "$@"; do
	case "$a" in
	--update-skips) update=1 ;;
	memory | sqlite | postgres) stores+=("$a") ;;
	*) echo "test-matrix: unknown argument $a" >&2; exit 2 ;;
	esac
done
[ ${#stores[@]} -eq 0 ] && stores=(memory sqlite postgres)

work="$(mktemp -d)"
pgdb=""
admin_url=""
cleanup() {
	if [ -n "$pgdb" ]; then
		psql -q "$admin_url" -c "DROP DATABASE IF EXISTS $pgdb WITH (FORCE)" >/dev/null 2>&1 || echo "test-matrix: could not drop $pgdb" >&2
	fi
	rm -rf "$work"
}
trap cleanup EXIT
gopid=""
on_signal() {
	[ -n "$gopid" ] && kill -TERM "$gopid" 2>/dev/null && wait "$gopid" 2>/dev/null
	exit 130
}
trap on_signal INT TERM

summary=()
failed=0
newknown=""

run_store() {
	local store="$1" out rc status skips unknown
	out="$work/$store.out"
	local -a env_args
	env_args=(-u TEST_DATABASE_URL TEST_STORE="$store" TEST_MATRIX_STRICT=1)
	if [ "$store" = postgres ]; then
		local base="${TEST_DATABASE_URL:-$local_url}"
		admin_url="$base"
		# no database in the URL: connect to the maintenance database to create ours
		if ! pg_url_has_db "$base"; then admin_url="$(pg_url_with_db "$base" postgres)" || admin_url="$base"; fi
		if ! psql -q "$admin_url" -c 'select 1' >/dev/null 2>&1; then
			echo "test-matrix: postgres is not reachable (set TEST_DATABASE_URL, or start the :54329 server from _docs/AGENTS.md)" >&2
			summary+=("postgres  FAIL  not reachable")
			failed=1
			return
		fi
		pgdb="mg_matrix_$$"
		# swap the database name in the URL (also when it has no path); the URL is never printed
		local url
		if ! url="$(pg_url_with_db "$base" "$pgdb")"; then
			summary+=("postgres  FAIL  TEST_DATABASE_URL is not a postgres:// URL")
			failed=1
			return
		fi
		if ! psql -q "$admin_url" -c "CREATE DATABASE $pgdb" >/dev/null 2>&1 ||
			! psql -q -v ON_ERROR_STOP=1 "$url" -f "$root/backend/db/schema.sql" >/dev/null 2>&1; then
			summary+=("postgres  FAIL  could not create $pgdb")
			failed=1
			return
		fi
		env_args+=(TEST_DATABASE_URL="$url")
	else
		# the Postgres-only tests report "TEST_DATABASE_URL not set" skips here; they run in the postgres pass
		:
	fi
	echo "== $store: go test -p 1 -race ./... =="
	# run in the background so a TERM/INT sent to this script reaches go test
	(cd "$root/backend" && exec env "${env_args[@]}" go test -p 1 -race -v -timeout 40m ./... >"$out" 2>&1) &
	gopid=$!
	wait $gopid
	rc=$?
	gopid=""
	grep -E -e '^(FAIL|panic)|--- FAIL' "$out" | head -20
	local pass fail
	pass=$(grep -c -E -e '^ *--- PASS:' "$out")
	fail=$(grep -c -E -e '^ *--- FAIL:' "$out")
	sed -E -n 's/^ *--- SKIP: ([^ ]+).*/\1/p' "$out" | sort -u >"$work/$store.skips"
	skips=$(wc -l <"$work/$store.skips" | tr -d ' ')
	if [ $update = 1 ]; then
		sed "s/^/$store /" "$work/$store.skips" >>"$work/newknown"
		unknown=""
	else
		grep -E "^$store " "$known" 2>/dev/null | cut -d' ' -f2 | sort -u >"$work/$store.known"
		unknown=$(comm -23 "$work/$store.skips" "$work/$store.known")
	fi
	status=PASS
	if [ $rc -ne 0 ]; then status=FAIL; failed=1; fi
	if [ -n "$unknown" ]; then
		status=FAIL
		failed=1
		echo "test-matrix: $store has skips not listed in backend/known_skips.txt:" >&2
		echo "$unknown" | sed 's/^/  /' >&2
	fi
	summary+=("$(printf '%-9s %-5s pass=%s fail=%s skip=%s' "$store" "$status" "$pass" "$fail" "$skips")")
	if [ $rc -ne 0 ]; then
		cp "$out" "${TMPDIR:-/tmp}/test-matrix-$store.out"
		echo "test-matrix: $store full output: ${TMPDIR:-/tmp}/test-matrix-$store.out" >&2
	fi
}

: >"$work/newknown"
for s in "${stores[@]}"; do run_store "$s"; done

if [ $update = 1 ]; then
	{
		grep -v -E "^($(IFS='|'; echo "${stores[*]}")) " "$known" 2>/dev/null
		cat "$work/newknown"
	} | sort -u >"$known.new" && mv "$known.new" "$known"
	echo "test-matrix: rewrote $known"
fi

echo
echo "== test matrix summary (skips are test names, all listed in backend/known_skips.txt) =="
printf '%s\n' "${summary[@]}"
[ $failed -eq 0 ] && echo "matrix: PASS" || echo "matrix: FAIL"
exit $failed
