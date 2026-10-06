##@ Testing

.PHONY: test-unit
test-unit: ## Run the helm-unittest suites of the chart.
	helm unittest helm/gateway-api-config --file "tests/*_test.yaml"
