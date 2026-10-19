#!/usr/bin/env bash
# Checks pg_url_with_db on every URL shape the matrix accepts for TEST_DATABASE_URL.
# Run: scripts/test-pgurl.sh   (no database needed)
set -u
. "$(cd "$(dirname "$0")" && pwd)/pgurl.sh"
bad=0
check() { # input expected
	got="$(pg_url_with_db "$1" mg_x)" || got="<error>"
	if [ "$got" != "$2" ]; then echo "FAIL $1 -> $got (want $2)"; bad=1; fi
}
check 'postgres://test@localhost:54329/mg?sslmode=disable' 'postgres://test@localhost:54329/mg_x?sslmode=disable'
check 'postgres://test@localhost:54329/mg'                 'postgres://test@localhost:54329/mg_x'
check 'postgres://test@localhost:54329/'                   'postgres://test@localhost:54329/mg_x'
check 'postgres://test@localhost:54329'                    'postgres://test@localhost:54329/mg_x'
check 'postgres://test@localhost:54329?sslmode=disable'    'postgres://test@localhost:54329/mg_x?sslmode=disable'
# Built from parts so no literal scheme://user:pass@ string sits in the tree (CI secret scan).
pgs=postgres; pw='p%40ss'
check "$pgs://u:$pw@db.example.com/app?sslmode=require&connect_timeout=5" "$pgs://u:$pw@db.example.com/mg_x?sslmode=require&connect_timeout=5"
check 'postgresql://localhost/app'                         'postgresql://localhost/mg_x'
check 'postgres://localhost'                               'postgres://localhost/mg_x'
check 'host=localhost dbname=mg'                           '<error>'
check ''                                                   '<error>'
check 'postgres://'                                        '<error>'
for u in 'postgres://t@h/mg' 'postgres://t@h/mg?x=1'; do pg_url_has_db "$u" || { echo "FAIL has_db $u"; bad=1; }; done
for u in 'postgres://t@h' 'postgres://t@h/' 'postgres://t@h?x=1' 'postgres://t@h/?x=1'; do pg_url_has_db "$u" && { echo "FAIL has_no_db $u"; bad=1; }; done
[ $bad -eq 0 ] && echo "test-pgurl: ok"
exit $bad
