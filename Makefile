BIN        := bin/netpulse
PLIST_NAME := io.github.domimaller.netpulse
PLIST      := $(HOME)/Library/LaunchAgents/$(PLIST_NAME).plist
CONFIG     := $(HOME)/.config/netpulse/config.yaml

APP        := build/NetPulse.app
SWIFT_PKG  := macos/NetPulseBar

.PHONY: build run test example-config config agent-install agent-uninstall agent-logs app app-install app-run

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

# ---- macOS menu bar app ----------------------------------------------------
# NetPulse.app = SwiftUI menu bar app + the Go server bundled next to it.
# The app uses an already running netpulse if there is one, else starts its own.
# Needs Xcode or the Command Line Tools (xcode-select --install) for `swift`.

app:              ## build build/NetPulse.app
	swift build -c release --package-path $(SWIFT_PKG)
	go build -o build/netpulse ./cmd/netpulse
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp macos/Info.plist $(APP)/Contents/Info.plist
	cp "$$(swift build -c release --package-path $(SWIFT_PKG) --show-bin-path)/NetPulseBar" $(APP)/Contents/MacOS/NetPulseBar
	cp build/netpulse $(APP)/Contents/MacOS/netpulse
	codesign --force --sign - $(APP)/Contents/MacOS/netpulse
	codesign --force --sign - $(APP)
	@echo "built $(APP)"

app-install: app  ## install to ~/Applications and (re)start it
	-pkill -x NetPulseBar; sleep 1
	mkdir -p $(HOME)/Applications
	rm -rf $(HOME)/Applications/NetPulse.app
	cp -R $(APP) $(HOME)/Applications/
	open $(HOME)/Applications/NetPulse.app
	@echo "NetPulse is in your menu bar. Tick 'Launch at login' in its panel to keep it."

app-run: app      ## run the freshly built app without installing
	-pkill -x NetPulseBar; sleep 1
	open $(APP)
