# syntax=docker/dockerfile:1
#
# fileee-server — gehärtetes, rootless, shell-loses Container-Image.
# Zwei Stufen: (1) Go-Binary statisch bauen + statische Infisical-CLI holen,
# (2) alles in ein "distroless/static:nonroot" Runtime-Image kopieren, das
# weder Shell noch Paketmanager enthält.
#
# Referenz: docs/superpowers/specs/2026-07-24-fileee-server-design.md §9.

# =============================================================================
# Build-Stage
# =============================================================================
# go.mod pinnt "go 1.23.0" OHNE toolchain-Override (siehe `grep '^go \|toolchain' go.mod`)
# -> golang:1.23-bookworm erfüllt das exakt, kein GOTOOLCHAIN-Zwangsupgrade nötig
# und GOTOOLCHAIN=local ist deshalb unproblematisch (Go würde sonst versuchen,
# eine neuere Toolchain nachzuladen, wenn go.mod eine höhere Version verlangt).
FROM golang:1.23-bookworm AS build

# Infisical-CLI-Version.
#
# WICHTIG (Korrektur gegenüber Task-Brief/Design-Spec): Die dort genannte Version
# "v0.43.113" existiert NICHT als GitHub-Release — weder als Tag "v0.43.113" noch
# als "infisical-cli/v0.43.113" (verifiziert 2026-07-24 via GitHub-Releases-API
# UND `git ls-remote --tags https://github.com/Infisical/infisical.git`, beide
# ergebnislos). Der aktuell NEUESTE echte "infisical-cli/vX.Y.Z"-Tag ist v0.41.90.
# Dessen linux_amd64-Release wurde lokal heruntergeladen, die SHA-256-Summe gegen
# die von Infisical veröffentlichte `checksums.txt` geprüft (Treffer) und `file`/
# `ldd` bestätigten "statically linked" / "not a dynamic executable" — siehe
# task-16-report.md für die vollständige Verifikations-Session. Diese Stage
# verifiziert die Checksumme UND die Statisch-Eigenschaft erneut beim Build.
ARG INFISICAL_CLI_VERSION=0.41.90
ARG INFISICAL_CLI_SHA256=ff67e4b0f5fafe1b33ea1606767e6235a8742cc47e6af634cdff54b8d1fccc0a

WORKDIR /src

# Go-Modul-Cache zuerst kopieren (Docker-Layer-Caching: `go mod download` läuft
# nur erneut, wenn sich go.mod/go.sum ändern, nicht bei jeder Quelltext-Änderung).
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 erzwingt ein vollstatisches Go-Binary (kein libc-Link) — Pflicht
# für das shell- und libc-lose distroless/static-Runtime-Image weiter unten.
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /fileee-server ./cmd/fileee-server

# Statische Infisical-CLI holen (offizielles Release-Binary, kein Drittanbieter-
# Mirror), Checksumme UND Staticness verifizieren, bevor sie ins Runtime-Image
# wandert. `ldd` auf einem echten statischen Binary meldet "not a dynamic
# executable" und liefert Exit-Code 1 — deshalb `|| true` und Text-Grep statt
# Exit-Code-Prüfung.
RUN set -eux; \
    curl -fsSL -o /tmp/infisical.tar.gz \
      "https://github.com/Infisical/infisical/releases/download/infisical-cli/v${INFISICAL_CLI_VERSION}/infisical_${INFISICAL_CLI_VERSION}_linux_amd64.tar.gz"; \
    echo "${INFISICAL_CLI_SHA256}  /tmp/infisical.tar.gz" | sha256sum -c -; \
    mkdir -p /tmp/infisical-extract; \
    tar -xzf /tmp/infisical.tar.gz -C /tmp/infisical-extract; \
    mv /tmp/infisical-extract/infisical /infisical; \
    chmod 755 /infisical; \
    ( ldd /infisical 2>&1 || true ) | grep -q "not a dynamic executable" \
      || (echo "FEHLER: infisical-Binary ist NICHT statisch verlinkt" >&2; exit 1)

# =============================================================================
# Runtime-Stage: distroless, rootless, shell-los
# =============================================================================
# Kein Shell, kein Paketmanager, kein Root — nur die zwei statischen Binaries.
# Enthält das CA-Zertifikats-Bundle (Pflicht für HTTPS gegen my.fileee.com und
# ggf. secretsmanager.strausmann.cloud im Infisical-Dual-Mode).
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build --chmod=755 /fileee-server /infisical /

# uid 65532 = distroless "nonroot"-Standard-User (kein eigener USER-Eintrag
# nötig/möglich, da kein /etc/passwd editierbar ist — das Basis-Image bringt
# diesen Nutzer bereits mit).
USER 65532

EXPOSE 8080

# Kein Shell im Image -> kein `CMD ["sh","-c","curl ..."]` möglich. Der Server
# bringt dafür ein eigenes Subcommand mit (cmd/fileee-server/main.go:
# `healthcheck` macht einen GET auf 127.0.0.1:<port>/healthz und liefert
# Exit-Code 0/1 — exec-Form, kein Shell-Aufruf).
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/fileee-server", "healthcheck"]

ENTRYPOINT ["/fileee-server"]
