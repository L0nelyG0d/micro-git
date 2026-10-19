# Sourced by test-matrix.sh and test-pgurl.sh.
# pg_url_with_db URL NAME prints URL with its database replaced by NAME (added
# when URL has no path), keeping scheme, user:password@host:port and ?query.
# Returns 1 for anything that is not a postgres:// or postgresql:// URL.
pg_url_with_db() {
	local u="$1" name="$2" scheme rest authority tail q=""
	case "$u" in
	postgres://* | postgresql://*) ;;
	*) return 1 ;;
	esac
	scheme="${u%%://*}"
	rest="${u#*://}"
	authority="${rest%%[/?]*}"
	[ -n "$authority" ] || return 1
	tail="${rest#"$authority"}"
	case "$tail" in
	*\?*) q="?${tail#*\?}" ;;
	esac
	printf '%s://%s/%s%s\n' "$scheme" "$authority" "$name" "$q"
}

# pg_url_has_db URL succeeds when URL names a database (non-empty path).
pg_url_has_db() {
	local rest="${1#*://}" authority
	authority="${rest%%[/?]*}"
	rest="${rest#"$authority"}"
	rest="${rest%%\?*}"
	[ -n "${rest#/}" ]
}
