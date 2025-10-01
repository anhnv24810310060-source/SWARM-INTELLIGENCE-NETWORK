#!/usr/bin/env bash
set -euo pipefail

HEADER='// Copyright (c) 2025 SwarmGuard
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//     http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.'

PY_HEADER='"""\nCopyright (c) 2025 SwarmGuard
Licensed under the Apache License, Version 2.0 (the "License");
You may not use this file except in compliance with the License.
You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
Distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.\n"""'

SH_HEADER='# Copyright (c) 2025 SwarmGuard\n# Licensed under the Apache License, Version 2.0 (the "License");\n# you may not use this file except in compliance with the License.\n# You may obtain a copy of the License at\n#   http://www.apache.org/licenses/LICENSE-2.0\n# Unless required by applicable law or agreed to in writing, software\n# distributed under the License is distributed on an "AS IS" BASIS,\n# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.\n# See the License for the specific language governing permissions and\n# limitations under the License.'

add_header() {
  local file="$1" ext
  ext="${file##*.}"
  case "$ext" in
    rs|go)
      if ! grep -q 'Apache License' "$file" 2>/dev/null; then
        printf '%s\n\n' "$HEADER" | cat - "$file" >"$file.new" && mv "$file.new" "$file"
        echo "[fix-license] added header to $file"
      fi
      ;;
    py)
      if ! grep -q 'Apache License' "$file" 2>/dev/null; then
        printf '%s\n\n' "$PY_HEADER" | cat - "$file" >"$file.new" && mv "$file.new" "$file"
        echo "[fix-license] added header to $file"
      fi
      ;;
    sh)
      if ! grep -q 'Apache License' "$file" 2>/dev/null; then
        printf '%s\n\n' "$SH_HEADER" | cat - "$file" >"$file.new" && mv "$file.new" "$file"
        echo "[fix-license] added header to $file"
      fi
      ;;
  esac
}

find . -type f \( -name '*.rs' -o -name '*.go' -o -name '*.py' -o -name '*.sh' \) \
  -not -path '*/target/*' -not -path '*/.git/*' | while read -r f; do
  add_header "$f"
 done

echo "[fix-license] complete"
