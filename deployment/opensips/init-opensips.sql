-- Provision a dedicated OpenSIPS database inside the existing Postgres
-- cluster. The actual schema lives in deployment/opensips/schema.sql and is
-- applied by the opensips-init container the first time the overlay is started.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'opensips') THEN
        CREATE ROLE opensips WITH LOGIN PASSWORD 'opensips';
    END IF;
END$$;

SELECT 'CREATE DATABASE opensips OWNER opensips'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'opensips')\gexec
