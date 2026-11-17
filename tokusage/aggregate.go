package tokusage

type modelKey struct {
	model    string
	provider string
	client   string
}

type modelAgg struct {
	tokens Usage
	calls  int64
}

type clientAgg struct {
	tokens Usage
	count  int64
	models map[[2]string]*modelAgg // {model, provider}
}

type dayAgg struct {
	tokens  Usage
	count   int64
	clients map[string]*clientAgg
}

type hourAgg struct {
	tokens  Usage
	count   int64
	clients map[string]bool
	models  map[[2]string]*modelAgg // {model, provider}
}

type aggregate struct {
	days   map[string]*dayAgg  // "2006-01-02"
	hours  map[string]*hourAgg // "2006-01-02 15:00:00"
	models map[modelKey]*modelAgg
	total  Usage
	count  int64
}

func newAggregate() *aggregate {
	return &aggregate{
		days:   map[string]*dayAgg{},
		hours:  map[string]*hourAgg{},
		models: map[modelKey]*modelAgg{},
	}
}

func (a *aggregate) add(e Entry) {
	t := e.Time.Local()
	date := t.Format("2006-01-02")
	hour := t.Format("2006-01-02 15:00:00")
	mp := [2]string{e.Model, e.Provider}

	day := a.days[date]
	if day == nil {
		day = &dayAgg{clients: map[string]*clientAgg{}}
		a.days[date] = day
	}
	day.tokens = day.tokens.add(e.Usage)
	day.count++
	client := day.clients[e.Client]
	if client == nil {
		client = &clientAgg{models: map[[2]string]*modelAgg{}}
		day.clients[e.Client] = client
	}
	client.tokens = client.tokens.add(e.Usage)
	client.count++
	dayModel := client.models[mp]
	if dayModel == nil {
		dayModel = &modelAgg{}
		client.models[mp] = dayModel
	}
	dayModel.tokens = dayModel.tokens.add(e.Usage)
	dayModel.calls++

	h := a.hours[hour]
	if h == nil {
		h = &hourAgg{clients: map[string]bool{}, models: map[[2]string]*modelAgg{}}
		a.hours[hour] = h
	}
	h.tokens = h.tokens.add(e.Usage)
	h.count++
	h.clients[e.Client] = true
	hourModel := h.models[mp]
	if hourModel == nil {
		hourModel = &modelAgg{}
		h.models[mp] = hourModel
	}
	hourModel.tokens = hourModel.tokens.add(e.Usage)
	hourModel.calls++

	key := modelKey{model: e.Model, provider: e.Provider, client: e.Client}
	m := a.models[key]
	if m == nil {
		m = &modelAgg{}
		a.models[key] = m
	}
	m.tokens = m.tokens.add(e.Usage)
	m.calls++

	a.total = a.total.add(e.Usage)
	a.count++
}
