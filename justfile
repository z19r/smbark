default:
    @just --list

build:
    go build -o smbark .

run: build
    ./smbark

install: build
    sudo cp smbark /usr/local/bin/

clean:
    rm -f smbark

vet:
    go vet ./...

check: vet build

tidy:
    go mod tidy

fmt:
    gofmt -w .

lint:
    golangci-lint run ./...

loc:
    @wc -l $(find . -name '*.go' -not -path './vendor/*') | tail -1

# Site
site-dev:
    cd site && python3 -m http.server 8080

site-deploy:
    netlify deploy --prod --dir=site

site-preview:
    netlify deploy --dir=site
