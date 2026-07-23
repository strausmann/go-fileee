#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

# HINWEIS zu den Zeilen/Spalten unten: "go tool cover -func" liest die ECHTE Quelldatei
# fileee/errors.go von der Platte, um je Funktion die Statement-Extents per AST zu ermitteln —
# ein rein synthetisches Profil mit frei erfundenen Zeilen (z.B. innerhalb eines var-Blocks) führt
# NICHT zu den erwarteten Prozentwerten (verifiziert: liefert 0.0% für alle drei echten Funktionen
# in errors.go, weil kein Statement in deren echten Zeilen getroffen wird). Deshalb zeigen die beiden
# Blöcke unten auf die ECHTEN Return-Statements der beiden Error()-Methoden in errors.go (Zeile 25
# bzw. 40) — count=1 markiert sie als gedeckt. Die dritte echte Funktion in der Datei
# (parseAPIError, Zeile 57) bleibt vom Profil unberührt und erscheint deshalb mit 0.0%.
# Ergebnis: 2 von 3 Funktionen gedeckt (100% + 100% + 0%) / 3 = 66,7% Durchschnitt.
cat > "$tmpdir/cover.out" <<'EOF'
mode: set
github.com/strausmann/go-fileee/fileee/errors.go:25.2,25.53 1 1
github.com/strausmann/go-fileee/fileee/errors.go:40.2,40.87 1 1
EOF

# Schwelle 40% -> muss erfüllt sein (66,7% Durchschnitt >= 40%)
if ! ./scripts/coverage-gate-strict.sh "$tmpdir/cover.out" fileee/errors.go:40; then
    echo "FEHLER: Gate hätte bei 66,7% Coverage / 40% Schwelle grün sein müssen" >&2
    exit 1
fi

# Schwelle 90% -> muss fehlschlagen (66,7% Durchschnitt < 90%)
if ./scripts/coverage-gate-strict.sh "$tmpdir/cover.out" fileee/errors.go:90; then
    echo "FEHLER: Gate hätte bei 66,7% Coverage / 90% Schwelle rot sein müssen" >&2
    exit 1
fi

# Kein-Treffer-Fall (Finding "Silent skip on filename mismatch"): ein Pfad, der im Coverprofil
# NICHT vorkommt (Tippfehler oder umbenannte/entfernte Datei in der Schwellen-Liste), darf das
# Gate NICHT stillschweigend durchwinken oder mittendrin abbrechen — es muss ein lautes FAIL mit
# dem Pfadnamen ausgeben UND mit Exit != 0 enden.
ausgabe=$(./scripts/coverage-gate-strict.sh "$tmpdir/cover.out" fileee/nicht-existent.go:40 2>&1) && rc=0 || rc=$?
if [[ "$rc" -eq 0 ]]; then
    echo "FEHLER: Gate hätte bei einem im Profil nicht vorhandenen Pfad rot sein müssen" >&2
    exit 1
fi
if ! grep -q "FAIL: fileee/nicht-existent.go" <<<"$ausgabe"; then
    echo "FEHLER: Gate hätte ein lautes FAIL für den unbekannten Pfad ausgeben müssen, Ausgabe war:" >&2
    echo "$ausgabe" >&2
    exit 1
fi

echo "coverage-gate-strict.sh Selbsttest: OK"
