serve:
	go run ./ serve

clean:
	rm -rf ./pb_data

build:
	go build -o bin/pocketbase-backend .

stripe-webhook:
	stripe listen --forward-to 127.0.0.1:8090/stripe/webhook
