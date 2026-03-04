CREATE TABLE articles (id INT AUTO_INCREMENT PRIMARY KEY, title VARCHAR(255), description VARCHAR(255), `text` TEXT, tags TEXT, pub_date DATETIME, mod_date DATETIME, priority BOOL, breaking_news BOOL, comment_status VARCHAR(255), photo_url VARCHAR(255));
CREATE TABLE authors (id INT AUTO_INCREMENT PRIMARY KEY, display_name VARCHAR(255), first_name VARCHAR(255), last_name VARCHAR(255), email VARCHAR(255), login VARCHAR(255));
CREATE TABLE articles_authors (id INT AUTO_INCREMENT PRIMARY KEY, author_id INT, articles_id INT);
