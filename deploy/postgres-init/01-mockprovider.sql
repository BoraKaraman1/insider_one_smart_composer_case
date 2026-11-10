-- Runs once, when the postgres volume is created (docker-entrypoint-initdb.d).
-- The mock provider gets its own role and database, and that role cannot
-- connect to the app database, so the provider cannot read messages or users.
-- An older volume lacks this database: recreate it with docker compose down -v.
-- Local dev credentials only.
CREATE ROLE mockprovider LOGIN PASSWORD 'mockprovider';
CREATE DATABASE mockprovider OWNER mockprovider;
REVOKE CONNECT ON DATABASE app FROM PUBLIC;
REVOKE CONNECT ON DATABASE mockprovider FROM PUBLIC;
GRANT CONNECT ON DATABASE mockprovider TO mockprovider;
