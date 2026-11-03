.PHONY: test build run-api run-search-indexer run-collector run-tahagasht-collector run-snapp-collector run-flytoday-collector run-mrbilit-collector tidy

test:
	go test ./...

build:
	go build -o bin/flight-services-api ./services/api/cmd/api
	go build -o bin/flight-services-alibaba-collector ./services/collectors/alibaba/cmd/collector
	go build -o bin/flight-services-tahagasht-collector ./services/collectors/tahagasht/cmd/collector
	go build -o bin/flight-services-snapp-collector ./services/collectors/snapp/cmd/collector
	go build -o bin/flight-services-flytoday-collector ./services/collectors/flytoday/cmd/collector
	go build -o bin/flight-services-mrbilit-collector ./services/collectors/mrbilit-collector/cmd/collector
	go build -o bin/flight-services-search-indexer ./services/search-indexer/cmd/indexer

tidy:
	go mod tidy

run-api:
	go run ./services/api/cmd/api

run-search-indexer:
	go run ./services/search-indexer/cmd/indexer

run-collector:
	go run ./services/collectors/alibaba/cmd/collector

run-tahagasht-collector:
	go run ./services/collectors/tahagasht/cmd/collector

run-snapp-collector:
	go run ./services/collectors/snapp/cmd/collector

run-flytoday-collector:
	go run ./services/collectors/flytoday/cmd/collector

run-mrbilit-collector:
	go run ./services/collectors/mrbilit-collector/cmd/collector
