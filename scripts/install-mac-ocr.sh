#!/bin/sh

set -eu

version="1.1.1"
archive_url="https://registry.npmjs.org/mac-ocr/-/mac-ocr-${version}.tgz"
expected_sha512="2b5xNapCpbjAjntPZmP8nVEDe8zzOXVUAOerh36azjuJqZfDTt9y8flwCVrcG+0GL1ox4UcWUE4PtGH34ChChw=="
install_dir="${MAC_OCR_INSTALL_DIR:-${HOME}/.local/bin}"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/paperless-macos-ocr.XXXXXX")"

cleanup() {
	rm -rf -- "${temporary_dir}"
}
trap cleanup EXIT HUP INT TERM

archive_path="${temporary_dir}/mac-ocr.tgz"
extract_dir="${temporary_dir}/extract"

curl --proto '=https' --tlsv1.2 --location --fail --silent --show-error \
	"${archive_url}" -o "${archive_path}"

actual_sha512="$(openssl dgst -sha512 -binary "${archive_path}" | openssl base64 -A)"
if [ "${actual_sha512}" != "${expected_sha512}" ]; then
	echo "mac-ocr archive integrity check failed" >&2
	exit 1
fi

mkdir -p "${extract_dir}" "${install_dir}"
tar -xzf "${archive_path}" -C "${extract_dir}" package/bin/mac-ocr
install -m 0755 "${extract_dir}/package/bin/mac-ocr" "${install_dir}/mac-ocr"

installed_version="$("${install_dir}/mac-ocr" --version)"
if [ "${installed_version}" != "${version}" ]; then
	echo "installed mac-ocr reported unexpected version: ${installed_version}" >&2
	exit 1
fi

echo "Installed mac-ocr ${version} to ${install_dir}/mac-ocr"
echo "Configure MAC_OCR_PATH=${install_dir}/mac-ocr"
