package provider

// Purpose names the kind of model call so a cheaper model can serve some of them later.
type Purpose string

const (
	PurposeChat     Purpose = "chat"
	PurposeClassify Purpose = "classify" // extension point: small cheap classifier model
	PurposeExtract  Purpose = "extract"  // vision extraction
	PurposeUI       Purpose = "ui"       // UI-agent planning / vision fallback
)

// Router picks the provider and model for a purpose. Today every purpose uses the
// default provider; register an override to route e.g. PurposeClassify elsewhere.
type Router struct {
	Default   AIProvider
	overrides map[Purpose]route
}

type route struct {
	p     AIProvider
	model string
}

func NewRouter(def AIProvider) *Router { return &Router{Default: def, overrides: map[Purpose]route{}} }

// Override routes a purpose to another provider/model (not used yet).
func (r *Router) Override(p Purpose, ai AIProvider, model string) { r.overrides[p] = route{ai, model} }

// For returns the provider and the model to request for a purpose.
func (r *Router) For(p Purpose, fallbackModel string) (AIProvider, string) {
	if o, ok := r.overrides[p]; ok {
		return o.p, o.model
	}
	if fallbackModel == "" {
		fallbackModel = r.Default.DefaultModel()
	}
	return r.Default, fallbackModel
}
