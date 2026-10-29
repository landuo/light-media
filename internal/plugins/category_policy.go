package plugins

// These finite resource methods connect the CategoryEditor business worker to
// the existing application policy and volatile MessageHelper queue. They never
// grant an arbitrary filesystem path, configuration namespace or notification
// channel. Each call still needs the standard SDK's active object authority.
const (
	CategoryDefaultCapability  = "plugin.category.get_default"
	CategoryApplyCapability    = "plugin.category.apply"
	SystemMessagePutCapability = "plugin.systemmessage.put"
)
