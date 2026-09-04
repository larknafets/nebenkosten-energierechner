# Zähler-Verschachtelung und PV-Verrechnung beim Strom

Ergänzt die Formeln in [README.md](../README.md#strom-pv-netzbezug-zuteilung) und [Berechnungslogik](../internal/web/templates/berechnungslogik.html) um eine visuelle Übersicht.

## Zähler-Verschachtelung

```
Zähler
├─ Strom
│  ├─ Netzbezug Gesamt (strom_gesamt)       ← vom Netz bezogen, NETTO nach PV-Eigenverbrauch
│  ├─ Wohnung 2 (strom_wohnung2)            ← Unterzähler, ROHER Verbrauch (vor PV-Abzug)
│  ├─ Wärmepumpe (strom_waermepumpe)        ← Unterzähler, ROHER Verbrauch (vor PV-Abzug)
│  ├─ Wallboxen (strom_wallbox)             ← Unterzähler, ROHER Verbrauch (vor PV-Abzug)
│  └─ Einspeisung (strom_einspeisung)       ← PV-Überschuss ins Netz
├─ Wärme
│  ├─ Wohnung 1 (waerme_wohnung1)           ← Wärmemengenzähler
│  └─ Wohnung 2 (waerme_wohnung2)           ← Wärmemengenzähler
└─ Wasser
   ├─ Gesamt (wasser_gesamt)
   ├─ Wohnung 2 (wasser_wohnung2)
   └─ Warmwasseraufbereitung (wasser_warmwasseraufbereitung)
```

Wohnung 1's Strom hat **keinen eigenen Zähler** - er ergibt sich implizit als Rest der Kaskade unten.

## PV-Verrechnungskaskade

Reihenfolge ist fix: Wohnung 2 → Wärmepumpe → Wallboxen → Rest = Wohnung 1.

```
Netzbezug Gesamt (netto)
        │
        ▼
┌───────────────────────┐  strom_wohnung2 (roh) ──┐
│ Stufe 1: Wohnung 2      │                          │
│ Anteil = min(Netzbezug, W2-roh)                    │
└───────────┬────────────┘                          │
   Rest1 = Netzbezug − W2-Anteil        PV-Anteil W2 = W2-roh − W2-Anteil
        │                                (nur >0, falls Netzbezug < W2-roh)
        ▼
┌───────────────────────┐  strom_waermepumpe (roh) ┐
│ Stufe 2: Wärmepumpe     │                          │
│ Anteil = min(Rest1, WP-roh)      ← Basis für Heizungskosten (70/30-Split)
└───────────┬────────────┘                          │
   Rest2 = Rest1 − WP-Anteil          PV-Anteil WP = WP-roh − WP-Anteil
        │
        ▼
┌───────────────────────┐  strom_wallbox (roh) ────┐
│ Stufe 3: Wallboxen      │                          │
│ Anteil = min(Rest2, Wallbox-roh)   ← rein informativ, keine Wohnungs-Zuteilung
└───────────┬────────────┘                          │
   Rest3 = Rest2 − Wallbox-Anteil    PV-Anteil Wallbox = Wallbox-roh − Wallbox-Anteil
        │
        ▼
   Wohnung 1 = Rest3
   (kompletter verbleibender Netzbezug, kein eigener Zähler/PV-Anteil)
```

**Kernidee:** Jede Stufe bekommt zuerst ihren vollen eigenen Verbrauch abgerechnet, solange Netzbezug reicht. Reicht er nicht, deckt PV die Lücke (`PV-Anteil`, angezeigt als "nicht dem Netzbezug zugeordnet (PV)", kostet nichts). Was nach allen 3 Stufen an Netzbezug übrig bleibt, geht implizit komplett an Wohnung 1 - dort gibt es keinen eigenen Zähler, also auch keinen sichtbaren PV-Anteil für Wohnung 1 selbst (der steckt bereits unsichtbar im Unterschied zwischen Netzbezug Gesamt und der Summe aller Rohverbräuche).

Siehe `internal/calc/strom.go` für die Implementierung.
