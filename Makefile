# Run everything in one go
test:
	docker compose -f docker-compose.test.yml up -d --wait
	go test -v ./...
	docker compose -f docker-compose.test.yml down

# Keep the DB running while you write code/debug
db-up:
	docker compose -f docker-compose.test.yml up -d

db-down:
	docker compose -f docker-compose.test.yml down