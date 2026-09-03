# Nebenkostenrechner

Web-App zur monatlichen Nebenkostenabrechnung für ein Zweifamilienhaus mit Wärmepumpe und PV-Anlage. Berechnet Strom-, Heizung/Warmwasser- und Wasserkosten je Wohnung aus monatlich erfassten Zählerständen, sowie Fixkosten/Grundgebühren (Grundsteuer, Versicherung, Deichbeiträge, Abfallwirtschaft, Grundpreise, Wärmepumpen-Wartung) aus einer separaten monatlichen Erfassung.

Details und Entscheidungshistorie: [Spec-Map (Issue #1)](https://github.com/larknafets/nebenkostenrechner/issues/1).

## Stammdaten

Auf der `/stammdaten`-Seite gepflegt - aktuelle Einzelwerte, nicht pro Monat historisiert, wirken sofort auf alle Berechnungen:

| Wohnung | Wohnungsgröße | Flurstücksgröße |
|---|---|---|
| Wohnung 1 | 116,23 m² | - |
| Wohnung 2 | 86 m² | - |

Dort auch die 14 Fixkosten-Kostenpositionen, jahresweise gepflegt (Berechnungslogik, Typ jährlich/monatlich, ggf. Jahreswert) - siehe [Fixkosten/Grundgebühren](#fixkostengrundgebühren) unten.

Personenzahl ist variabel und wird separat pro Ablesung *und* pro Fixkosten-Eingabe erfasst (zwei unabhängige Werte, nicht gemeinsam versioniert).

Preise (aktuell, werden pro Monat neu erfasst statt zentral versioniert):

| Kostenart | Preis |
|---|---|
| Strom | 0,22 EUR/kWh |
| Frischwasser | 1,46 EUR/m³ |
| Abwasser | 4,87 EUR/m³ |
| Einspeisung (PV) | 0,08 EUR/kWh |

## Zähler

| Key | Beschreibung | Einheit |
|---|---|---|
| `strom_gesamt` | Stromzähler Gesamt (Netzbezug) | kWh |
| `strom_wohnung2` | Zwischenstromzähler Wohnung 2 | kWh |
| `strom_waermepumpe` | Zwischenstromzähler Wärmepumpe | kWh |
| `strom_wallbox` | Zwischenzähler Wallboxen | kWh |
| `wasser_gesamt` | Wasserzähler Gesamt | m³ |
| `wasser_wohnung2` | Zwischenwasserzähler Wohnung 2 | m³ |
| `wasser_warmwasseraufbereitung` | Zwischenwasserzähler Warmwasseraufbereitung | m³ |
| `waerme_wohnung1` | Wärmemengenzähler Wohnung 1 | **MWh** |
| `waerme_wohnung2` | Wärmemengenzähler Wohnung 2 | **MWh** |
| `strom_einspeisung` | Einspeisezähler (PV) | kWh |

Ablese-Rhythmus: 1x/Monat. Verbrauch = aktueller Zählerstand minus Stand der chronologisch nächst-älteren Ablesung (einfache Differenz, funktioniert automatisch auch über Lücken hinweg).

`strom_wallbox` wird aktuell in keiner Formel verwendet - Wallbox-Nutzung ist derzeit ausschließlich Wohnung 1 zugeordnet und läuft implizit in deren Reststrom mit.

## Berechnungslogik

### Strom (PV-Netzbezug-Zuteilung)

Netzbezug wird sequenziell zugeteilt: zuerst Wohnung 2 (gedeckelt auf ihren eigenen Verbrauch), dann die Wärmepumpe auf den verbleibenden Netzbezug, der Rest zählt implizit zu Wohnung 1 (keine eigene Kostenposition).

```
Netzbezug_Gesamt = Verbrauch(strom_gesamt)

W2_Anteil_kWh = min(Netzbezug_Gesamt, Verbrauch(strom_wohnung2))
Rest1          = Netzbezug_Gesamt - W2_Anteil_kWh
WP_Anteil_kWh  = min(Rest1, Verbrauch(strom_waermepumpe))

Kosten_Strom_W2       = W2_Anteil_kWh * Strompreis
Kosten_WP_gesamt      = WP_Anteil_kWh * Strompreis   (Basis für Heizungskosten, siehe unten)
```

Bei PV-Überschuss (`Netzbezug_Gesamt = 0`) sind beide Kosten 0. Alle Anteile über `min()` gedeckelt, nie negativ.

### Heizung/Warmwasser (konfigurierbarer Split, Default 70/30)

Die Wärmepumpen-Stromkosten (siehe oben) werden nach Wärmemengenzähler-Verhältnis und Wohnungsgrößen-Verhältnis auf die beiden Wohnungen verteilt. Die Gewichtung wird pro Periode im Wizard gewählt (70/30, 60/40 oder 50/50 - kein Freitext, siehe Issue #27) und ab dann für diese Periode eingefroren. `qm_W1`/`qm_W2` kommen dagegen live von den Stammdaten, nicht von der Periode.

```
Ratio_Waerme_W1  = Verbrauch(waerme_wohnung1) / (Verbrauch(waerme_wohnung1) + Verbrauch(waerme_wohnung2))
Ratio_Waerme_W2  = Verbrauch(waerme_wohnung2) / (Verbrauch(waerme_wohnung1) + Verbrauch(waerme_wohnung2))
Ratio_Flaeche_W1 = qm_W1 / (qm_W1 + qm_W2)
Ratio_Flaeche_W2 = qm_W2 / (qm_W1 + qm_W2)

Kosten_Heizung_W1 = Kosten_WP_gesamt * (Gewichtung_Waerme * Ratio_Waerme_W1 + Gewichtung_Flaeche * Ratio_Flaeche_W1)
Kosten_Heizung_W2 = Kosten_WP_gesamt * (Gewichtung_Waerme * Ratio_Waerme_W2 + Gewichtung_Flaeche * Ratio_Flaeche_W2)
```

### Wasser (Frischwasser + Abwasser)

Der Verbrauch für die Warmwasseraufbereitung wird nach dem Personenverhältnis der jeweiligen Ablesung auf beide Wohnungen umgelegt, bevor die Frischwasserkosten berechnet werden. Abwasser wird in gleicher Menge wie Frischwasser angenommen (kein separater Abwasserzähler).

```
WW_Anteil_W1 = Verbrauch(wasser_warmwasseraufbereitung) * Personen_W1 / (Personen_W1 + Personen_W2)
WW_Anteil_W2 = Verbrauch(wasser_warmwasseraufbereitung) * Personen_W2 / (Personen_W1 + Personen_W2)

Frischwasser_W2 = Verbrauch(wasser_wohnung2) + WW_Anteil_W2
Frischwasser_W1 = (Verbrauch(wasser_gesamt) - Verbrauch(wasser_wohnung2) - Verbrauch(wasser_warmwasseraufbereitung)) + WW_Anteil_W1

Abwasser_W1 = Frischwasser_W1
Abwasser_W2 = Frischwasser_W2

Kosten_Frischwasser_X = Frischwasser_X * Frischwasserpreis
Kosten_Abwasser_X     = Abwasser_X * Abwasserpreis
```

### Einspeisevergütung (PV)

Rein informativ, unabhängig von der Kostenverteilung oben - keine Wohnungs-Zuteilung, da der Einspeisezähler haus-weit misst.

```
Einspeisevergütung = Verbrauch(strom_einspeisung) * Einspeisung_Preis
```

### Fixkosten/Grundgebühren

Die 14 festen Kostenpositionen (Grundsteuer, Wohngebäudeversicherung, Deichbeitrag Grund und Boden, Deichbeitrag Bauliche Anlagen, Kreisverband Wesermarsch, Abfallwirtschaft Grundgebühr Haushalt/Personen/Biomüll/Restmüll, Grundpreis Strom, Grundgebühr Trinkwasser/Abwasser, Grundpreis Internet, Wärmepumpen-Wartung) werden unabhängig von Strom/Heizung/Wasser auf einer eigenen monatlichen Fixkosten-Eingabe erfasst und berechnet. Jede Position hat pro Jahr (Stammdaten) einen Typ:

```
Monatswert("jährlich")  = Jahreswert / 12                       (Stammdaten, im Formular nicht editierbar)
Monatswert("monatlich") = expliziter Wert der Fixkosten-Eingabe  (fehlt er, z.B. nach Typwechsel:
                                                                   letzter_bekannter_Jahreswert / 12, sonst 0)
```

Aufteilung auf Wohnung 1/2 je nach Berechnungslogik (ebenfalls pro Position/Jahr in den Stammdaten gewählt):

```
Je Wohneinheit             : 50 / 50
Je anteiliges Flurstück    : flurstueck_W1 / (flurstueck_W1 + flurstueck_W2)   (Stammdaten)
Je anteilige Wohnungsgröße : qm_W1 / (qm_W1 + qm_W2)                            (Stammdaten)
Je Anzahl Personen         : Personen_W1 / (Personen_W1 + Personen_W2)         (Fixkosten-Eingabe)
```

Sind bei den letzten drei Logiken beide Werte 0, fällt die Aufteilung auf hälftig zurück (gleiche Regel wie bei der Heizungs-Wärme-Ratio).

### Rundung

Jede Kostenposition (Strom, Heizung/Warmwasser, Frischwasser, Abwasser, jede der 14 Fixkosten-Positionen) wird einzeln je Wohnung **kaufmännisch auf Cent gerundet** (0,5 Cent immer aufgerundet), erst nach der vollständigen Berechnung mit float-Genauigkeit. Die angezeigte Gesamtsumme je Wohnung kann dadurch um 1-2 Cent von der rechnerisch exakten Summe abweichen - das ist akzeptiert, es gibt keinen Korrekturmechanismus.

### Preise & Personenzahl

Es gibt kein zentrales Preishistorie-Konzept: Strompreis, Frischwasser- und Abwasserpreis sowie die Personenzahl je Wohnung werden direkt bei jeder monatlichen Ablesung mit erfasst (nicht separat versioniert). Ein einmal berechneter Monat bleibt dadurch automatisch "eingefroren", auch wenn sich Preise oder Personenzahl später ändern.

Die Fixkosten-Eingabe hat ihre eigene, unabhängige Personenzahl je Wohnung (ebenfalls pro Monat eingefroren) - muss nicht mit der Ablesung übereinstimmen. Wohnungsgröße und Flurstücksgröße sind dagegen keine monatlichen Werte mehr, sondern aktuelle Stammdaten-Einzelwerte, die sofort auf alle Monate wirken.

### Fehlerbehandlung bei Zähleranomalien

Reine Warnhinweise, kein hartes Blockieren (Single-User-App ohne Vier-Augen-Prinzip):

- **Negativer Verbrauch** (neuer Stand < Vorstand): Warnung, Speichern bleibt möglich.
- **Ausreißer**: Warnung, wenn der Verbrauch eines Zählers um mehr als ±50% vom Durchschnitt seiner letzten 3 Ablesungen abweicht (bei <3 Vorwerten kein Check).
- **Fehlende Ablesung** (Lücke >1 Monat): Hinweis "Verbrauch über X Monate", Berechnung läuft automatisch über den längeren Zeitraum.

## Datenmodell

SQLite, kein ORM (`modernc.org/sqlite` + `database/sql`):

```
apartments(id, name, qm, flurstueck_groesse)
meters(id, key UNIQUE, type, unit, apartment_id NULLABLE -> apartments.id, label)
periods(id, reading_date DATE, strompreis, frischwasser_preis, abwasser_preis, heizung_waerme_gewichtung, einspeisung_preis)
meter_readings(id, period_id -> periods.id, meter_id -> meters.id, zaehlerstand, UNIQUE(period_id, meter_id))
period_occupancy(id, period_id -> periods.id, apartment_id -> apartments.id, personen, UNIQUE(period_id, apartment_id))

kostenpositionen(id, key UNIQUE, label)
kostenpositionen_jahre(id, kostenposition_id -> kostenpositionen.id, jahr, logik, typ, jahreswert, UNIQUE(kostenposition_id, jahr))
fixkosten_eingaben(id, monat DATE)
fixkosten_werte(id, fixkosten_eingabe_id -> fixkosten_eingaben.id, kostenposition_id -> kostenpositionen.id, wert, UNIQUE(fixkosten_eingabe_id, kostenposition_id))
fixkosten_personen(id, fixkosten_eingabe_id -> fixkosten_eingaben.id, apartment_id -> apartments.id, personen, UNIQUE(fixkosten_eingabe_id, apartment_id))
```

Berechnete Kosten werden nicht persistiert, sondern bei jedem Aufruf live aus den Rohdaten berechnet.

## Tech-Stack

Go, `modernc.org/sqlite` (kein ORM, `database/sql`), server-rendered `html/template` mit Vanilla-JS für die Wizard-Interaktivität (kein htmx - ursprünglich in der Spec vorgesehen, aber nicht gebraucht). SQLite-Datei unter `/data` im Container. Multi-Arch-Docker-Image (`linux/amd64`, `linux/arm64`, `linux/arm/v7`) auf Basis `gcr.io/distroless/static-debian12`, läuft als root. Version und Build-Datum werden per `-ldflags` eingebrannt und im Dashboard angezeigt (Ticket #48).

## Installation

### Docker

```bash
docker run -d \
  --name nebenkostenrechner \
  -p 8080:8080 \
  -v nebenkosten-data:/data \
  ghcr.io/larknafets/nebenkostenrechner:latest
```

Danach erreichbar unter `http://localhost:8080`, Health-Check unter `/healthz`. Die Image-Tags folgen den Release-Versionen - siehe [Releases](https://github.com/larknafets/nebenkostenrechner/releases).

| Umgebungsvariable | Default | Beschreibung |
|---|---|---|
| `DB_PATH` | `/data/nebenkosten.db` | Pfad zur SQLite-Datenbankdatei |
| `LISTEN_ADDR` | `:8080` | Listen-Adresse des HTTP-Servers |

### Home Assistant Add-on

Für den Betrieb als Home Assistant Add-on (inkl. Ingress-Integration, siehe Issue #22) siehe das Add-on-Repository [`larknafets/ha-addons`](https://github.com/larknafets/ha-addons).
