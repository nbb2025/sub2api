#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM

cat > "$test_dir/env" <<'ENV'
POSTGRES_PASSWORD=compose-render-test
DATABASE_HOST=database.invalid
DATABASE_PASSWORD=compose-render-test
REDIS_HOST=cache.invalid
ENV

render() {
  image=$1
  output=$2
  shift 2
  SUB2API_IMAGE="$image" docker compose --env-file "$test_dir/env" \
    --project-directory "$repo_root/deploy" "$@" config --format json > "$output"
}

for template in docker-compose.yml docker-compose.local.yml docker-compose.standalone.yml; do
  base="$repo_root/deploy/$template"
  render '' "$test_dir/default.json" -f "$base"
  render 'ghcr.io/nbb2025/sub2api:wan3' "$test_dir/tag.json" -f "$base"
  pinned='ghcr.io/nbb2025/sub2api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
  render "$pinned" "$test_dir/digest.json" -f "$base"

  # Reproduce an older installation whose application image is hardcoded.
  python3 - "$base" "$test_dir/old.yml" <<'PY'
import pathlib
import sys
pathlib.Path(sys.argv[2]).write_text(pathlib.Path(sys.argv[1]).read_text().replace(
    '${SUB2API_IMAGE:-weishaw/sub2api:latest}', 'weishaw/sub2api:latest'))
PY
  render "$pinned" "$test_dir/override.json" -f "$test_dir/old.yml" \
    -f "$repo_root/deploy/docker-compose.image.yml"

  python3 - "$test_dir" "$pinned" <<'PY'
import json
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
expected = {
    'default': 'weishaw/sub2api:latest',
    'tag': 'ghcr.io/nbb2025/sub2api:wan3',
    'digest': sys.argv[2],
    'override': sys.argv[2],
}
baseline = None
for name, image in expected.items():
    config = json.loads((root / (name + '.json')).read_text())
    assert config['services']['sub2api'].pop('image') == image, name
    if baseline is None:
        baseline = config
    assert config == baseline, name + ' changed database, configuration, or persistence'
PY
done

if render '' "$test_dir/invalid.json" -f "$base" \
  -f "$repo_root/deploy/docker-compose.image.yml" 2> "$test_dir/error.log"; then
  printf 'image override must reject a missing SUB2API_IMAGE\n' >&2
  exit 1
fi

printf 'docker compose image override test passed\n'
