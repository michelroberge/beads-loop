BINARY   := beads-loop
INSTALL  := /usr/local/bin

.PHONY: build install uninstall clean

build:
	go build -o $(BINARY) .

install: build
	sudo cp $(BINARY) $(INSTALL)/$(BINARY)
	@echo "installed: $(INSTALL)/$(BINARY)"

uninstall:
	sudo rm -f $(INSTALL)/$(BINARY)

clean:
	rm -f $(BINARY)
