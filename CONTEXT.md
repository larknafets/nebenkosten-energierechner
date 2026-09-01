# Nebenkostenrechner

Monatliche Nebenkostenabrechnung für ein Zweifamilienhaus mit Wärmepumpe und PV-Anlage: Strom-, Heizungs- und Wasserkosten werden aus monatlich erfassten Zählerständen berechnet und auf Wohnung 1/Wohnung 2 verteilt.

## Language

**Ablesung**:
Eine monatliche Erfassung: Zählerstände, Preise, Personenzahl je Wohnung und Heizungs-Gewichtung zu einem Ablesedatum. Im Code auch "Periode" genannt.
_Avoid_: Eintrag, Datensatz

**Zählerstand**:
Der am Ablesedatum abgelesene Wert eines Zählers - ein kumulativer Punktwert, kein Verbrauch.
_Avoid_: Messwert, Reading

**Verbrauch**:
Die Differenz zwischen dem Zählerstand einer Ablesung und dem der chronologisch nächst-älteren Ablesung desselben Zählers.

**Netzbezug**:
Die vom Stromnetz bezogene Energiemenge (Zähler `strom_gesamt`) - bereits netto nach PV-Eigenverbrauch, da PV-Eigenverbrauch nie durch diesen Zähler läuft.

**Nicht dem Netzbezug zugeordnet (PV)**:
Die kWh-Differenz zwischen dem Verbrauch eines Zwischenzählers (Wohnung 2 oder Wärmepumpe) und dem Anteil, den die Zuteilungslogik ihm tatsächlich vom Netzbezug angerechnet hat. Übersteigt der Zwischenzähler-Verbrauch den verbleibenden Netzbezug, kann die Differenz nur durch PV gedeckt worden sein - eine aus der Zuteilungslogik abgeleitete Näherung, kein gemessener PV-Wert (es gibt keinen PV-Eigenverbrauchszähler).
_Avoid_: PV-Anteil, PV-Ertrag (das ist die separate Einspeisevergütung, siehe unten)

**Wohnung 1 / Wohnung 2**:
Die zwei Hausparteien, auf die Kosten verteilt werden. Wohnung 1 trägt keine eigene Strom-Kostenposition - ihr Netzbezug bleibt implizit der Rest nach Wohnung 2 und Wärmepumpe.

## Heizung & Warmwasser

**WP-Strom (Heizung + Warmwasser)**:
Die der Wärmepumpe angerechnete Netzbezug-Menge (kWh) - die Kostenbasis für die Heizungskosten. Deckt Raumheizung UND Warmwasserbereitung gemeinsam ab; die Wärmemengenzähler messen nur Raumheizung, können den Anteil also nicht trennen.
_Avoid_: Heizungsstrom (verschleiert, dass Warmwasser mit drin steckt)

**Wärmeverbrauch**:
Die von den Wärmemengenzählern (`waerme_wohnung1`/`waerme_wohnung2`) gemessene Wärmemenge in MWh - reine Raumheizung, keine Warmwasserbereitung.

**Heizungs-Gewichtung**:
Der pro Ablesung gewählte Split (70/30, 60/40 oder 50/50), nach dem der WP-Strom zwischen Wärmeverbrauchs-Verhältnis und Wohnflächen-Verhältnis der beiden Wohnungen gewichtet wird.

**Warmwasseraufbereitung**:
Ein Wasser-Begriff (Zähler `wasser_warmwasseraufbereitung`), nicht zu verwechseln mit "WP-Strom (Heizung + Warmwasser)" oben - hier geht es um Frischwasser-Verbrauch für die Aufbereitung, nicht um Wärmepumpen-Strom.

## PV

**Einspeisung / Einspeisevergütung**:
Die ins Netz eingespeiste PV-Überschussmenge (Zähler `strom_einspeisung`) und die dafür gezahlte Vergütung. Rein informativ, haus-weit, unabhängig von der Kostenverteilung - nicht zu verwechseln mit "Nicht dem Netzbezug zugeordnet (PV)" oben, das eine Kosten-interne Zuteilungsgröße ist.
