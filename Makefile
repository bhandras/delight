SHELL := /bin/bash

SIMULATOR_DEVICE ?= iPhone 16
CONFIGURATION ?= Debug
DERIVED_DATA ?= /tmp/delight-ios
BUNDLE_ID ?= com.bhandras.delight.harness
APP_PATH := $(DERIVED_DATA)/Build/Products/$(CONFIGURATION)-iphonesimulator/DelightHarness.app

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
	xcrun simctl boot "$(SIMULATOR_DEVICE)" || true
	xcrun simctl bootstatus "$(SIMULATOR_DEVICE)" -b

ios-install: ios-build ios-sim-boot
	xcrun simctl install booted "$(APP_PATH)"

ios-run: ios-install
	xcrun simctl launch booted "$(BUNDLE_ID)"

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
