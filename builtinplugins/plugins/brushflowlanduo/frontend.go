package brushflowlanduo

import sdk "light-media/builtinplugins/internal/sdk"

func Frontend() *sdk.Frontend {
	return &sdk.Frontend{
		Contract:    sdk.FrontendFederationContract,
		Root:        "dist/assets",
		Entry:       "remoteEntry.js",
		Exposes:     []string{"Config", "Page", "Dashboard", "AppPage"},
		Shared:      []string{"vue", "vuetify", "vuetify/styles"},
		APIContract: sdk.FrontendAPIContract,
	}
}
