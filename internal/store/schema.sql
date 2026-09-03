CREATE TABLE IF NOT EXISTS apartments (
    id                 INTEGER PRIMARY KEY,
    name               TEXT NOT NULL,
    qm                 REAL NOT NULL,
    flurstueck_groesse REAL NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS meters (
    id           INTEGER PRIMARY KEY,
    key          TEXT NOT NULL UNIQUE,
    type         TEXT NOT NULL,
    unit         TEXT NOT NULL,
    apartment_id INTEGER REFERENCES apartments(id),
    label        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS periods (
    id                         INTEGER PRIMARY KEY,
    reading_date               TEXT NOT NULL,
    strompreis                 REAL NOT NULL,
    frischwasser_preis         REAL NOT NULL,
    abwasser_preis             REAL NOT NULL,
    heizung_waerme_gewichtung  REAL NOT NULL DEFAULT 0.7,
    einspeisung_preis          REAL NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS meter_readings (
    id           INTEGER PRIMARY KEY,
    period_id    INTEGER NOT NULL REFERENCES periods(id),
    meter_id     INTEGER NOT NULL REFERENCES meters(id),
    zaehlerstand REAL NOT NULL,
    UNIQUE(period_id, meter_id)
);

CREATE TABLE IF NOT EXISTS period_occupancy (
    id           INTEGER PRIMARY KEY,
    period_id    INTEGER NOT NULL REFERENCES periods(id),
    apartment_id INTEGER NOT NULL REFERENCES apartments(id),
    personen     INTEGER NOT NULL,
    UNIQUE(period_id, apartment_id)
);

CREATE TABLE IF NOT EXISTS kostenpositionen (
    id    INTEGER PRIMARY KEY,
    key   TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS kostenpositionen_jahre (
    id                 INTEGER PRIMARY KEY,
    kostenposition_id  INTEGER NOT NULL REFERENCES kostenpositionen(id),
    jahr               INTEGER NOT NULL,
    logik              TEXT NOT NULL,
    typ                TEXT NOT NULL,
    jahreswert         REAL NOT NULL DEFAULT 0,
    UNIQUE(kostenposition_id, jahr)
);

CREATE TABLE IF NOT EXISTS fixkosten_eintraege (
    id    INTEGER PRIMARY KEY,
    monat TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS fixkosten_werte (
    id                    INTEGER PRIMARY KEY,
    fixkosten_eintrag_id  INTEGER NOT NULL REFERENCES fixkosten_eintraege(id),
    kostenposition_id     INTEGER NOT NULL REFERENCES kostenpositionen(id),
    wert                  REAL NOT NULL,
    UNIQUE(fixkosten_eintrag_id, kostenposition_id)
);

CREATE TABLE IF NOT EXISTS fixkosten_personen (
    id                    INTEGER PRIMARY KEY,
    fixkosten_eintrag_id  INTEGER NOT NULL REFERENCES fixkosten_eintraege(id),
    apartment_id          INTEGER NOT NULL REFERENCES apartments(id),
    personen              INTEGER NOT NULL,
    UNIQUE(fixkosten_eintrag_id, apartment_id)
);
