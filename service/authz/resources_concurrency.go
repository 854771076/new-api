package authz

const (
	ResourceConcurrency = "concurrency"

	ActionConcurrencyRead  = "read"
	ActionConcurrencyWrite = "write"
)

var (
	ConcurrencyRead  = Permission{Resource: ResourceConcurrency, Action: ActionConcurrencyRead}
	ConcurrencyWrite = Permission{Resource: ResourceConcurrency, Action: ActionConcurrencyWrite}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceConcurrency,
		LabelKey: "Concurrency Limits",
		Actions: []ActionDefinition{
			{
				Action:       ActionConcurrencyRead,
				LabelKey:     "View concurrency limits and live in-flight counters",
				DefaultRoles: []string{BuiltInRoleAdmin},
			},
			{
				Action:       ActionConcurrencyWrite,
				LabelKey:     "Edit per-user / per-API-key concurrency limits",
				DefaultRoles: []string{BuiltInRoleAdmin},
			},
		},
	})
}
