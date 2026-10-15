#!/bin/sh
# Regenerates the golden fixtures with real git. Run from backend/gitobj/testdata.
# Output: raw object bodies (tree.bin, commit0.bin, commit1.bin) and oids.txt.
set -e
D=$(mktemp -d)
git init -q "$D"
cd "$D"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
mkdir foo sub sub/deep
printf 'a\n' > a.txt
printf 'foo file\n' > foo.txt
printf 'inside foo\n' > foo/x.txt
printf 'deep\n' > sub/deep/d.txt
printf 'dash\n' > foo-bar
git add -A
TREE=$(git write-tree)
export GIT_AUTHOR_NAME=alice GIT_AUTHOR_EMAIL=alice@micro-git.local GIT_AUTHOR_DATE="1700000000 +0000"
export GIT_COMMITTER_NAME=alice GIT_COMMITTER_EMAIL=alice@micro-git.local GIT_COMMITTER_DATE="1700000000 +0000"
C0=$(printf 'first commit\n' | git commit-tree "$TREE")
printf 'b\n' > b.txt
git add -A
TREE1=$(git write-tree)
export GIT_AUTHOR_DATE="1700000100 +0000" GIT_COMMITTER_DATE="1700000100 +0000"
C1=$(printf 'second commit\n' | git commit-tree "$TREE1" -p "$C0")
OUT="$OLDPWD"
git cat-file tree "$TREE" > "$OUT/tree.bin"
git cat-file commit "$C0" > "$OUT/commit0.bin"
git cat-file commit "$C1" > "$OUT/commit1.bin"
{
echo "tree $TREE"
echo "commit0 $C0"
echo "commit1 $C1"
echo "tree1 $TREE1"
echo "a.txt $(git hash-object a.txt)"
echo "foo.txt $(git hash-object foo.txt)"
echo "foo/x.txt $(git hash-object foo/x.txt)"
echo "foo-bar $(git hash-object foo-bar)"
echo "sub/deep/d.txt $(git hash-object sub/deep/d.txt)"
echo "foo $(git rev-parse "$TREE":foo)"
echo "sub $(git rev-parse "$TREE":sub)"
echo "sub/deep $(git rev-parse "$TREE":sub/deep)"
echo "tree1 $TREE1"
echo "b.txt $(git hash-object b.txt)"
echo "empty-tree $(git hash-object -t tree /dev/null)"
} > "$OUT/oids.txt"
rm -rf "$D"
