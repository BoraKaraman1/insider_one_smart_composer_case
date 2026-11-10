-- Sahte sağlayıcının kendi rolü var ve uygulama veritabanına giremez.
CREATE ROLE mockprovider LOGIN PASSWORD 'mockprovider';
CREATE DATABASE mockprovider OWNER mockprovider;
REVOKE CONNECT ON DATABASE app FROM PUBLIC;
REVOKE CONNECT ON DATABASE mockprovider FROM PUBLIC;
GRANT CONNECT ON DATABASE mockprovider TO mockprovider;
