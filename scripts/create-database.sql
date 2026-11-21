-- Creates the development and test databases and the application user.
-- Run once:  mysql -u root -p < scripts/create-database.sql

CREATE DATABASE IF NOT EXISTS gotaticket CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
CREATE DATABASE IF NOT EXISTS gotaticket_test CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;

CREATE USER IF NOT EXISTS 'gotaticket'@'localhost' IDENTIFIED BY 'gotaticket';
CREATE USER IF NOT EXISTS 'gotaticket'@'127.0.0.1' IDENTIFIED BY 'gotaticket';

GRANT ALL PRIVILEGES ON gotaticket.* TO 'gotaticket'@'localhost', 'gotaticket'@'127.0.0.1';
GRANT ALL PRIVILEGES ON gotaticket_test.* TO 'gotaticket'@'localhost', 'gotaticket'@'127.0.0.1';
FLUSH PRIVILEGES;
