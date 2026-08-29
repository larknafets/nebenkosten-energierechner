# Nebenkosten-Energierechner

Web-App zur monatlichen Nebenkostenabrechnung für ein Zweifamilienhaus mit Wärmepumpe und PV-Anlage. Berechnet Strom-, Heizung/Warmwasser- und Wasserkosten je Wohnung aus monatlich erfassten Zählerständen.

Details und Entscheidungshistorie: [Spec-Map (Issue #1)](https://github.com/larknafets/nebenkosten-energierechner/issues/1).

## Stammdaten

| Wohnung | Wohnfläche | Personen (aktuell) |
|---|---|---|
| Wohnung 1 | 116,23 m² | 2 |
| Wohnung 2 | 86 m² | 1 |

Wohnfläche ändert sich praktisch nie, wird aber pro Ablesung mit erfasst. Personenzahl ist variabel und wird pro Ablesezeitraum historisiert (nicht als feste Konstante).

Preise (aktuell, werden pro Monat neu erfasst statt zentral versioniert):

| Kostenart | Preis |
|---|---|
| Strom | 0,22 EUR/kWh |
| Frischwasser | 1,46 EUR/m³ |
| Abwasser | 4,87 EUR/m³ |

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

### Heizung/Warmwasser (70/30-Split)

Die Wärmepumpen-Stromkosten (siehe oben) werden zu 70% nach Wärmemengenzähler-Verhältnis und zu 30% nach Wohnflächen-Verhältnis auf die beiden Wohnungen verteilt.

```
Ratio_Waerme_W1  = Verbrauch(waerme_wohnung1) / (Verbrauch(waerme_wohnung1) + Verbrauch(waerme_wohnung2))
Ratio_Waerme_W2  = Verbrauch(waerme_wohnung2) / (Verbrauch(waerme_wohnung1) + Verbrauch(waerme_wohnung2))
Ratio_Flaeche_W1 = qm_W1 / (qm_W1 + qm_W2)
Ratio_Flaeche_W2 = qm_W2 / (qm_W1 + qm_W2)

Kosten_Heizung_W1 = Kosten_WP_gesamt * (0.7 * Ratio_Waerme_W1 + 0.3 * Ratio_Flaeche_W1)
Kosten_Heizung_W2 = Kosten_WP_gesamt * (0.7 * Ratio_Waerme_W2 + 0.3 * Ratio_Flaeche_W2)
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

### Rundung

Jede Kostenposition (Strom, Heizung/Warmwasser, Frischwasser, Abwasser) wird einzeln je Wohnung **kaufmännisch auf Cent gerundet** (0,5 Cent immer aufgerundet), erst nach der vollständigen Berechnung mit float-Genauigkeit. Die angezeigte Gesamtsumme je Wohnung kann dadurch um 1-2 Cent von der rechnerisch exakten Summe abweichen - das ist akzeptiert, es gibt keinen Korrekturmechanismus.

### Preise & Personenzahl

Es gibt kein zentrales Preishistorie-Konzept: Strompreis, Frischwasser- und Abwasserpreis sowie die Personenzahl je Wohnung werden direkt bei jeder monatlichen Ablesung mit erfasst (nicht separat versioniert). Ein einmal berechneter Monat bleibt dadurch automatisch "eingefroren", auch wenn sich Preise oder Personenzahl später ändern.

### Fehlerbehandlung bei Zähleranomalien

Reine Warnhinweise, kein hartes Blockieren (Single-User-App ohne Vier-Augen-Prinzip):

- **Negativer Verbrauch** (neuer Stand < Vorstand): Warnung, Speichern bleibt möglich.
- **Ausreißer**: Warnung, wenn der Verbrauch eines Zählers um mehr als ±50% vom Durchschnitt seiner letzten 3 Ablesungen abweicht (bei <3 Vorwerten kein Check).
- **Fehlende Ablesung** (Lücke >1 Monat): Hinweis "Verbrauch über X Monate", Berechnung läuft automatisch über den längeren Zeitraum.

## Datenmodell

SQLite, kein ORM (`modernc.org/sqlite` + `database/sql`):

```
apartments(id, name, qm)
meters(id, key UNIQUE, type, unit, apartment_id NULLABLE -> apartments.id, label)
periods(id, reading_date DATE, strompreis, frischwasser_preis, abwasser_preis)
meter_readings(id, period_id -> periods.id, meter_id -> meters.id, zaehlerstand, UNIQUE(period_id, meter_id))
period_occupancy(id, period_id -> periods.id, apartment_id -> apartments.id, personen, UNIQUE(period_id, apartment_id))
```

Berechnete Kosten werden nicht persistiert, sondern bei jedem Aufruf live aus den Rohdaten berechnet.

## Tech-Stack

Go, `modernc.org/sqlite` (kein ORM), server-rendered `html/template` mit Vanilla-JS für die Wizard-Interaktivität (kein htmx - ursprünglich in der Spec vorgesehen, aber nicht gebraucht), ein Docker-Container mit SQLite unter `/data`. Einbindung als Home Assistant Addon über `larknafets/ha-addons` (Ingress-Integration umgesetzt, siehe Issue #22), analog `larknafets/gcs-connector-evcc`.

## Umsetzung

Implementierungs-Tickets: [#10–#22](https://github.com/larknafets/nebenkosten-energierechner/issues?q=is%3Aissue+label%3Aready-for-agent).
