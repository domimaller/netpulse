BIN        := bin/netpulse
PLIST_NAME := io.github.domimaller.netpulse
PLIST      := $(HOME)/Library/LaunchAgents/$(PLIST_NAME).plist
CONFIG     := $(HOME)/.config/netpulse/config.yaml

.PHONY: build run test example-config config agent-install agent-uninstall agent-logs

build:            ## build bin/netpulse
	go build -o $(BIN) ./cmd/netpulse

run:              ## run in the foreground (UI at http://127.0.0.1:7171)
	go run ./cmd/netpulse

test:
	go vet ./... && go test ./...

example-config:   ## regenerate netpulse.example.yaml from the embedded defaults
	cp internal/config/default.yaml netpulse.example.yaml

config:           ## create ~/.config/netpulse/config.yaml (all defaults, documented) if missing
	@mkdir -p $(dir $(CONFIG))
	@test -f $(CONFIG) && echo "$(CONFIG) already exists" || (cp internal/config/default.yaml $(CONFIG) && echo "wrote $(CONFIG)")

agent-install: build   ## start at login + keep running (macOS LaunchAgent)
	@mkdir -p $(HOME)/Library/LaunchAgents $(HOME)/Library/Logs
	sed -e "s|@BIN@|$(abspath $(BIN))|g" -e "s|@HOME@|$(HOME)|g" -e "s|@LABEL@|$(PLIST_NAME)|g" \
		packaging/macos/netpulse.plist.tmpl > $(PLIST)
	-launchctl bootout gui/$$(id -u) $(PLIST) 2>/dev/null
	launchctl bootstrap gui/$$(id -u) $(PLIST)
	@echo "netpulse is running: http://127.0.0.1:7171  (logs: ~/Library/Logs/netpulse.log)"

agent-uninstall:
	-launchctl bootout gui/$$(id -u) $(PLIST)
	rm -f $(PLIST)

agent-logs:
	tail -f $(HOME)/Library/Logs/netpulse.log
