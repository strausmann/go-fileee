---
name: Feature-Request
about: Eine neue Funktion für go-fileee vorschlagen
title: "feat: "
labels: enhancement
---

## Problem / Motivation

Welches Problem löst der Vorschlag? Warum reicht die aktuelle Lib dafür nicht aus?

## Vorschlag

Wie könnte die Lösung aussehen (API-Form, neue Methode/Option, geändertes Verhalten)?

## Domänen-Neutralität beachten

go-fileee ist **domänen-neutral** — kein Ziel-/Fremdsystem-Code im Repo (siehe
[ADR-0006](../../docs/adr/0006-domaenen-neutralitaet.md)). Integrationen wie eine
Paperless-/DMS-Migration gehören in ein **externes** Projekt, das go-fileee als Lib importiert,
nicht in dieses Repo. Bitte kurz einordnen:

- [ ] Der Vorschlag betrifft die generische Fileee-API-Abdeckung (Auth, Entities, Download,
      Upload) — passt in dieses Repo.
- [ ] Der Vorschlag ist zielsystem-spezifisch (z. B. DMS-Mapping) — gehört in ein externes
      Consumer-Projekt, nicht hierher.

## Alternativen

Welche Alternativen wurden erwogen (z. B. Lösung im Consumer-Projekt statt in der Lib)?

## Zusätzlicher Kontext

Links, verwandte Issues, Beispiele aus anderen Bibliotheken.
