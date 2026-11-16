#!/usr/bin/env bash
# Offline tests for the data-disk part of deploy/aws/remote-deploy.sh (#207): find_data_disk and
# prepare_data_disk, with lsblk/findmnt/blkid/mkfs/mount/systemctl/docker replaced by shell functions
# and /dev by a temp directory. No root, no disk, no AWS. Usage: bash deploy/aws/test-remote-deploy.sh
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
fails=0; n=0
ok()   { n=$((n+1)); echo "ok   $n $1"; }
bad()  { n=$((n+1)); fails=$((fails+1)); echo "FAIL $n $1"; }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

VOL=vol-0123456789abcdef0

# Fresh fake machine: /dev, the data dir, fstab and the stub state live under $T/<case>.
setup() { # case-name
  C="$T/$1"; mkdir -p "$C/dev/disk/by-id" "$C/state"
  : > "$C/fstab"; : > "$C/log"
  export MG_DEV="$C/dev" MG_DATA_DIR="$C/opt/mg/data" MG_FSTAB="$C/fstab" MG_SLEEP=0
  DEV="$MG_DEV"; DATA_DIR="$MG_DATA_DIR"   # remote-deploy.sh reads these once, when sourced
  ROOT_SRC="$C/dev/nvme0n1p1"; ROOT_DISK=nvme0n1
  : > "$C/dev/nvme0n1"; : > "$C/dev/nvme0n1p1"
}
nitro_disk() { : > "$C/dev/nvme1n1"; ln -s ../../nvme1n1 "$C/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_${VOL/-/}"; }

# --- stubs (functions win over commands of the same name) ---
findmnt() { # findmnt -no SOURCE <path>
  local p="${*: -1}"
  if [ "$p" = / ]; then echo "$ROOT_SRC"; elif [ -f "$C/state/mounted" ]; then cat "$C/state/mounted"; else return 1; fi
}
lsblk() { # lsblk -no PKNAME <dev> | lsblk -no NAME <dev>
  case "$2" in
    PKNAME) [ "$3" = "$ROOT_SRC" ] && echo "$ROOT_DISK" ;;
    NAME) echo "$(basename "$3")"; [ -f "$C/state/partitions" ] && echo "$(basename "$3")p1" ;;
  esac; return 0
}
blkid() { # blkid -o value -s TYPE|UUID <dev>
  [ -f "$C/state/fstype" ] || return 2
  case "$4" in TYPE) cat "$C/state/fstype" ;; UUID) echo 11111111-2222-3333-4444-555555555555 ;; esac
}
mkfs.xfs() { echo "mkfs.xfs $*" >> "$C/log"; echo xfs > "$C/state/fstype"; }
mountpoint() { [ -f "$C/state/mounted" ]; }
mount() { echo "mount $*" >> "$C/log"; readlink -f "$C/dev/nvme1n1" > "$C/state/mounted" 2>/dev/null || echo "$C/dev/xvdf" > "$C/state/mounted"; }
systemctl() { echo "systemctl $*" >> "$C/log"; }
dnf() { echo "dnf $*" >> "$C/log"; }
docker() { # docker ps -q | docker inspect -f ... <ids>
  case "$1" in
    ps) [ -f "$C/state/container_mounts" ] && echo abc123 ; return 0 ;;
    inspect) cat "$C/state/container_mounts" ;;
  esac
}

# shellcheck source=deploy/aws/remote-deploy.sh
MG_REMOTE_DEPLOY_FUNCS_ONLY=1 source "$HERE/remote-deploy.sh"
set +e   # remote-deploy.sh sets -e; the tests check exit codes themselves

# 1. Nitro: the by-id link of the volume id resolves to the data disk.
setup nitro; nitro_disk
out="$(find_data_disk "$VOL")"
check "nitro: finds the disk by volume id" '[ "$out" = "$(readlink -f "$C/dev/nvme1n1")" ]'

# 2. A link that resolves to the root disk is never returned.
setup rootlink; ln -s ../../nvme0n1 "$C/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_${VOL/-/}"
find_data_disk "$VOL" >/dev/null; rc=$?
check "root disk is never the data disk" '[ "$rc" -ne 0 ]'

# 3. Another volume's link does not match.
setup other; : > "$C/dev/nvme1n1"; ln -s ../../nvme1n1 "$C/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol0fffffffffffffff0"
find_data_disk "$VOL" >/dev/null; rc=$?
check "another volume id does not match" '[ "$rc" -ne 0 ]'

# 4. Xen (t2): /dev/xvdf.
setup xen; ROOT_SRC="$C/dev/xvda1"; ROOT_DISK=xvda; : > "$C/dev/xvda"; : > "$C/dev/xvda1"; : > "$C/dev/xvdf"
out="$(find_data_disk "$VOL")"
check "xen: falls back to /dev/xvdf" '[ "$out" = "$(readlink -f "$C/dev/xvdf")" ]'

# 5. No device: prepare fails, nothing formatted or mounted.
setup none
prepare_data_disk "$VOL" >/dev/null 2>"$C/err"; rc=$?
check "no device: fails" '[ "$rc" -ne 0 ] && grep -q "no device found" "$C/err"'
check "no device: no mkfs, no mount" '! grep -q "mkfs\|^mount" "$C/log"'

# 6. Blank disk: formatted once, fstab line once, mounted; a second run changes nothing.
setup blank; nitro_disk
prepare_data_disk "$VOL" >"$C/out" 2>&1; rc=$?
check "blank disk: succeeds" '[ "$rc" -eq 0 ]'
check "blank disk: mkfs.xfs on the data disk" 'grep -q "mkfs.xfs -q $(readlink -f "$C/dev/nvme1n1")" "$C/log"'
check "blank disk: fstab line with nofail and docker ordering" 'grep -q "^UUID=11111111-2222-3333-4444-555555555555 $MG_DATA_DIR xfs defaults,nofail,x-systemd.before=docker.service,x-systemd.required-by=docker.service 0 2$" "$C/fstab"'
check "blank disk: mounted, backups dir created" 'grep -q "^mount $MG_DATA_DIR" "$C/log" && [ -d "$MG_DATA_DIR/backups" ]'
: > "$C/log"
prepare_data_disk "$VOL" >"$C/out" 2>&1; rc=$?
check "second run: succeeds, already mounted" '[ "$rc" -eq 0 ] && grep -q "already mounted" "$C/out"'
check "second run: no mkfs, no mount, one fstab line" '! grep -q "mkfs\|^mount" "$C/log" && [ "$(wc -l < "$C/fstab")" -eq 1 ]'

# 7. Existing XFS (data on it): mounted, never formatted.
setup existing; nitro_disk; echo xfs > "$C/state/fstype"
prepare_data_disk "$VOL" >/dev/null 2>&1; rc=$?
check "existing xfs: mounted without mkfs" '[ "$rc" -eq 0 ] && ! grep -q mkfs "$C/log" && grep -q "^mount" "$C/log"'

# 8. Another filesystem: refused, not formatted, not mounted.
setup ext4; nitro_disk; echo ext4 > "$C/state/fstype"
prepare_data_disk "$VOL" >/dev/null 2>"$C/err"; rc=$?
check "ext4: refused" '[ "$rc" -ne 0 ] && grep -q "expected xfs" "$C/err" && ! grep -q "mkfs\|^mount" "$C/log"'

# 9. Partitions but no filesystem: refused, not formatted.
setup parts; nitro_disk; : > "$C/state/partitions"
prepare_data_disk "$VOL" >/dev/null 2>"$C/err"; rc=$?
check "partitioned disk: refused, no mkfs" '[ "$rc" -ne 0 ] && grep -q partitions "$C/err" && ! grep -q mkfs "$C/log"'

# 10. Mounted from something else: refused.
setup othermount; nitro_disk; echo "$C/dev/nvme9n1" > "$C/state/mounted"
prepare_data_disk "$VOL" >/dev/null 2>"$C/err"; rc=$?
check "data dir mounted from another device: refused" '[ "$rc" -ne 0 ] && grep -q "not from the data volume" "$C/err"'

# 11. Old dumps on the root disk: moved aside and copied onto the disk.
setup olddumps; nitro_disk; mkdir -p "$MG_DATA_DIR/backups"; echo dump > "$MG_DATA_DIR/backups/mg-old.sql.gz"
mount() { echo "mount $*" >> "$C/log"; readlink -f "$C/dev/nvme1n1" > "$C/state/mounted"; }
prepare_data_disk "$VOL" >"$C/out" 2>&1; rc=$?
check "old dumps: succeeds" '[ "$rc" -eq 0 ]'
check "old dumps: moved aside and copied" 'ls -d "$MG_DATA_DIR".root-* >/dev/null 2>&1 && [ -f "$MG_DATA_DIR/backups/mg-old.sql.gz" ]'

# 12. A running container binds the root-disk data dir: refused, nothing moved.
setup inuse; nitro_disk; mkdir -p "$MG_DATA_DIR/pgdata"; echo x > "$MG_DATA_DIR/pgdata/PG_VERSION"
printf '%s\n' "$MG_DATA_DIR/pgdata" > "$C/state/container_mounts"
prepare_data_disk "$VOL" >/dev/null 2>"$C/err"; rc=$?
check "data dir in use by a container: refused" '[ "$rc" -ne 0 ] && grep -q "running container uses" "$C/err"'
check "data dir in use: not moved, not mounted" '[ -f "$MG_DATA_DIR/pgdata/PG_VERSION" ] && ! grep -q "^mount" "$C/log"'

# 13. Argument check of the real entry point (runs only up to the validation, then exits 1).
out="$(env -u MG_REMOTE_DEPLOY_FUNCS_ONLY bash "$HERE/remote-deploy.sh" s r i/x:y /dev/null abcdef1 vol-XYZ 2>&1)"; rc=$?
check "bad data-volume id is rejected" '[ "$rc" -ne 0 ] && printf "%s" "$out" | grep -q "data-volume-id must look like"'

echo
if [ "$fails" -eq 0 ]; then echo "all $n remote-deploy data-disk tests passed"; else echo "$fails of $n failed"; exit 1; fi
