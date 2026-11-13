#!/bin/sh

set -eu

# The pin and expected version live in the Nix files; read them from there.
repository_root="$(cd "$(dirname "$0")/.." && pwd)"
repository_url="https://github.com/beanieboi/mac-ocr.git"
repository_commit="$(sed -n 's|.*"github:beanieboi/mac-ocr/\([0-9a-f]\{40\}\)".*|\1|p' "${repository_root}/flake.nix")"
patched_version="$(sed -n 's|^  version = "\(.*\)";$|\1|p' "${repository_root}/nix/mac-ocr.nix")"
if [ -z "${repository_commit}" ] || [ -z "${patched_version}" ]; then
	echo "could not read the mac-ocr pin from flake.nix and nix/mac-ocr.nix" >&2
	exit 1
fi
install_dir="${MAC_OCR_INSTALL_DIR:-${HOME}/.local/bin}"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/paperless-vision-ocr.XXXXXX")"

cleanup() {
	rm -rf -- "${temporary_dir}"
}
trap cleanup EXIT HUP INT TERM

if ! command -v git >/dev/null 2>&1; then
	echo "Git is required to fetch the pinned mac-ocr fork commit." >&2
	exit 1
fi
if ! command -v swift >/dev/null 2>&1; then
	echo "Swift is required to build the patched mac-ocr binary." >&2
	echo "Install Xcode Command Line Tools with: xcode-select --install" >&2
	exit 1
fi

source_dir="${temporary_dir}/mac-ocr"
git init --quiet "${source_dir}"
git -C "${source_dir}" remote add origin "${repository_url}"
git -C "${source_dir}" fetch --quiet --depth 1 origin "${repository_commit}"
git -C "${source_dir}" checkout --quiet --detach FETCH_HEAD

resolved_commit="$(git -C "${source_dir}" rev-parse HEAD)"
if [ "${resolved_commit}" != "${repository_commit}" ]; then
	echo "mac-ocr checkout resolved to unexpected commit: ${resolved_commit}" >&2
	exit 1
fi

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

echo "Installed patched mac-ocr ${installed_version} to ${install_dir}/mac-ocr"
echo "Source: ${repository_url}@${repository_commit}"
echo "Configure MAC_OCR_PATH=${install_dir}/mac-ocr"
