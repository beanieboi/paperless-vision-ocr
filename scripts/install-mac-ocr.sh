#!/bin/sh

set -eu

version="1.1.1"
patched_version="1.1.1-paperless.1"
source_archive_url="https://github.com/privatenumber/mac-ocr/archive/refs/tags/v${version}.tar.gz"
expected_sha256="e430269f1dcaf49a5636f88737536212c78e2fb9cddb3e1eef03c1c2900993fd"
install_dir="${MAC_OCR_INSTALL_DIR:-${HOME}/.local/bin}"
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
patch_path="${script_dir}/../patches/mac-ocr-1.1.1-searchable-pdf-spacing.patch"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/paperless-macos-ocr.XXXXXX")"

cleanup() {
	rm -rf -- "${temporary_dir}"
}
trap cleanup EXIT HUP INT TERM

if ! command -v swift >/dev/null 2>&1; then
	echo "Swift is required to build the patched mac-ocr binary." >&2
	echo "Install Xcode Command Line Tools with: xcode-select --install" >&2
	exit 1
fi
if [ ! -f "${patch_path}" ]; then
	echo "mac-ocr patch not found: ${patch_path}" >&2
	exit 1
fi

archive_path="${temporary_dir}/mac-ocr-source.tar.gz"
source_dir="${temporary_dir}/mac-ocr-${version}"

curl --proto '=https' --tlsv1.2 --location --fail --silent --show-error \
	"${source_archive_url}" -o "${archive_path}"

actual_sha256="$(shasum -a 256 "${archive_path}" | awk '{print $1}')"
if [ "${actual_sha256}" != "${expected_sha256}" ]; then
	echo "mac-ocr source archive integrity check failed" >&2
	exit 1
fi

tar -xzf "${archive_path}" -C "${temporary_dir}"
patch --batch --forward --directory "${source_dir}" -p1 < "${patch_path}"

(
	cd "${source_dir}"
	swift build -c release --arch arm64 --arch x86_64 -Xswiftc -Osize -Xlinker -dead_strip
)

built_binary="${source_dir}/.build/apple/Products/Release/mac-ocr"
if [ ! -x "${built_binary}" ]; then
	echo "patched mac-ocr build did not produce the expected universal binary" >&2
	exit 1
fi

mkdir -p "${install_dir}"
install -m 0755 "${built_binary}" "${install_dir}/mac-ocr"

installed_version="$("${install_dir}/mac-ocr" --version)"
if [ "${installed_version}" != "${patched_version}" ]; then
	echo "installed mac-ocr reported unexpected version: ${installed_version}" >&2
	exit 1
fi

echo "Installed patched mac-ocr ${version} to ${install_dir}/mac-ocr"
echo "Patch: searchable PDF line text preserves recognized whitespace"
echo "Configure MAC_OCR_PATH=${install_dir}/mac-ocr"
