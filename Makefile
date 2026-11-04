up:
	cd ./infra && cp .env.example .env && sudo ./up.sh && cd ../flight-service && sudo docker compose up -d --build && cd ../ranker && sudo docker compose up -d --build && cd ../core-api && sudo docker compose up -d --build && cd ../flight-front && sudo docker compose up -d --build
