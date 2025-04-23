BINARY_NAME=nat-test

# Default target to run the application
default: run

# Target to build the application
build:
	go build -o $(BINARY_NAME)

# Target to run the application
run: build
	./$(BINARY_NAME)

# Target to clean up the binary
clean:
	rm -f $(BINARY_NAME)

.PHONY: default build run clean
