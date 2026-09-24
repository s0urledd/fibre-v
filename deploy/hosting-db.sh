#!/bin/sh
# hosting-db.sh — download or refresh the two IP databases the collector's
# hosting lookup reads (provider / network / country of every registered
# Fibre host). The collector itself never fetches anything: it reads these
# files, and with no ASN file the feature is simply off.
#
#   fibre-hosting-db /var/lib/fibre-observer/mocha/hosting   (installed from deploy/hosting-db.sh)
#
# Writes, atomically (download to a temp name, check, rename):
#
#   ip2asn-combined.tsv.gz     iptoasn.com, IPv4+IPv6 → origin AS, AS name,
#                              AS registry country. Public Domain (ODC PDDL
#                              v1.0), https://iptoasn.com/ — refreshed hourly
#                              upstream; daily here is plenty.
#   dbip-country-lite.csv.gz   DB-IP "IP to Country Lite", CC BY 4.0,
#                              https://db-ip.com/db/download/ip-to-country-lite
#                              — monthly upstream. The licence requires the
#                              credit "IP Geolocation by DB-IP" wherever the
#                              data is shown; the site prints it under the
#                              concentration panel. Skip it with
#                              HOSTING_SKIP_COUNTRY=1 and countries fall back to
#                              the AS registry's (the API says which).
#
# Both land in the directory given (default: ./hosting). Put it at
# <data-dir>/hosting and the collector finds them without any configuration;
# anywhere else, set HOSTING_ASN_DB / HOSTING_COUNTRY_DB in the network's env
# file. The collector notices a changed file (size or mtime) on its next
# endpoint poll and re-runs the lookup; no restart.
#
# Run it as the service user so the files are readable by the collector,
# e.g. from a monthly timer or cron:
#   sudo -u fibre-observer fibre-hosting-db /var/lib/fibre-observer/mocha/hosting
#
# Needs curl and gzip. Exits non-zero, leaving the previous files in place,
# when a download fails or does not look like the expected format.
set -eu

dest=${1:-./hosting}
mkdir -p "$dest"
tmp=$(mktemp -d "${dest%/}/.dl.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() { # url out
	curl -fsSL --retry 3 --max-time 300 -A "tensile-hosting-db/1 (+https://github.com/s0urledd/tensile)" -o "$2" "$1"
}

# check_lines file min_lines pattern: the file gunzips, has at least
# min_lines lines, and its first line matches pattern. A captive portal or
# an error page fails here instead of replacing a good database.
check() {
	n=$(gzip -dc "$1" | wc -l)
	first=$(gzip -dc "$1" | head -n 1)
	if [ "$n" -lt "$2" ]; then echo "hosting-db: $1: only $n lines" >&2; return 1; fi
	if ! printf '%s\n' "$first" | grep -Eq "$3"; then echo "hosting-db: $1: unexpected first line: $first" >&2; return 1; fi
	echo "hosting-db: $(basename "$1"): $n ranges"
}

# ---- ASN (required) ----
fetch https://iptoasn.com/data/ip2asn-combined.tsv.gz "$tmp/asn.gz"
check "$tmp/asn.gz" 100000 '^[0-9a-fA-F.:]+	[0-9a-fA-F.:]+	[0-9]+	'
mv -f "$tmp/asn.gz" "$dest/ip2asn-combined.tsv.gz"

# ---- country (optional) ----
if [ "${HOSTING_SKIP_COUNTRY:-0}" != "1" ]; then
	# DB-IP publishes one file per month; early in a month the new one may
	# not be up yet, so fall back to last month's.
	ok=0
	for m in "$(date -u +%Y-%m)" "$(date -u -d "$(date -u +%Y-%m-01) -1 day" +%Y-%m 2>/dev/null || true)"; do
		[ -n "$m" ] || continue
		if fetch "https://download.db-ip.com/free/dbip-country-lite-$m.csv.gz" "$tmp/country.gz" 2>/dev/null &&
			check "$tmp/country.gz" 100000 '^"?[0-9a-fA-F.:]+"?,"?[0-9a-fA-F.:]+"?,"?[A-Z]{2}"?$'; then
			mv -f "$tmp/country.gz" "$dest/dbip-country-lite.csv.gz"
			echo "hosting-db: DB-IP release $m"
			ok=1
			break
		fi
	done
	if [ "$ok" != 1 ]; then
		echo "hosting-db: DB-IP country file not refreshed (the previous one, if any, is kept)" >&2
		exit 1
	fi
fi
echo "hosting-db: done in $dest"
