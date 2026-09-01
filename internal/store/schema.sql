CREATE TABLE IF NOT EXISTS apartments (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    qm   REAL NOT NULL
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
