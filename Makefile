SHELL := /bin/bash

SIMULATOR_DEVICE ?= iPhone 16
CONFIGURATION ?= Debug
DERIVED_DATA ?= /tmp/delight-ios
BUNDLE_ID ?= com.bhandras.delight.harness
APP_PATH := $(DERIVED_DATA)/Build/Products/$(CONFIGURATION)-iphonesimulator/DelightHarness.app
SIMULATOR_UDID_FILE := $(DERIVED_DATA)/booted_simulator_udid

.PHONY: ios-sdk ios-build ios-sim-boot ios-install ios-run
GO_TEST_PKGS ?= ./sdk ./internal/crypto
GO_TEST_ARGS ?= -cover
IOS_TEST_RESULT ?= $(DERIVED_DATA)/TestResults

ios-sdk:
	./cli/scripts/build_ios_sdk.sh

ios-build: ios-sdk
	xcodebuild \
		-project ios/DelightHarness.xcodeproj \
		-scheme DelightHarness \
		-configuration $(CONFIGURATION) \
		-sdk iphonesimulator \
		-destination "platform=iOS Simulator,name=$(SIMULATOR_DEVICE)" \
		-derivedDataPath "$(DERIVED_DATA)" \
		build

ios-sim-boot:
	@mkdir -p "$(DERIVED_DATA)"
	@./cli/scripts/ensure_ios_sim_booted.sh "$(SIMULATOR_DEVICE)" > "$(SIMULATOR_UDID_FILE)"
	@echo "Booted simulator UDID: $$(cat "$(SIMULATOR_UDID_FILE)")"
	@UDID="$$(cat "$(SIMULATOR_UDID_FILE)")"; \
	open -a Simulator --args -CurrentDeviceUDID "$$UDID" >/dev/null 2>&1 || true

ios-install: ios-build ios-sim-boot
	@UDID="$$(cat "$(SIMULATOR_UDID_FILE)")"; \
	xcrun simctl install "$$UDID" "$(APP_PATH)"

ios-run: ios-install
	@UDID="$$(cat "$(SIMULATOR_UDID_FILE)")"; \
	xcrun simctl launch "$$UDID" "$(BUNDLE_ID)"

.PHONY: ios-test test

ios-test: ios-sdk ios-sim-boot
	rm -rf "$(IOS_TEST_RESULT)"
	rm -rf "$(IOS_TEST_RESULT).xcresult"
	xcodebuild \
		-project ios/DelightHarness.xcodeproj \
		-scheme DelightHarness \
		-configuration $(CONFIGURATION) \
		-sdk iphonesimulator \
		-destination "platform=iOS Simulator,name=$(SIMULATOR_DEVICE)" \
		-derivedDataPath "$(DERIVED_DATA)" \
		-resultBundlePath "$(IOS_TEST_RESULT)" \
		-enableCodeCoverage YES \
		test

test:
	(cd cli && go test $(GO_TEST_ARGS) $(GO_TEST_PKGS))
