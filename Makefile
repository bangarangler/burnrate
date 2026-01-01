.PHONY: build test lint run clean install-tools

build:
	mage build

test:
	mage test

lint:
	mage lint

run:
	mage run

clean:
	mage clean

install-tools:
	mage installTools

default: build
