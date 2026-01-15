serve:
	go run ./ serve

clean:
	rm -rf ./pb_data

build:
	go build -o bin/pocketbase-backend .