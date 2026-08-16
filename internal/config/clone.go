package config

// Clone returns a deep copy of the configuration. Every slice and map is
// reallocated so a clone and its source share no mutable state; publishing a
// clone is what makes a live reload safe for concurrent readers.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := *c
	out.Ignore = cloneStrings(c.Ignore)
	out.Frontmatter.Properties = cloneProperties(c.Frontmatter.Properties)
	out.Fzf.Args = cloneStrings(c.Fzf.Args)
	out.New.Routes = cloneStringMap(c.New.Routes)
	out.Related.MetadataProperties = cloneStrings(c.Related.MetadataProperties)
	out.Brief.ExcludeFolders = cloneStrings(c.Brief.ExcludeFolders)
	out.Brief.ExcludeTypes = cloneStrings(c.Brief.ExcludeTypes)
	out.Agent.Command = cloneStrings(c.Agent.Command)
	out.Cockpit.DisabledRules = cloneStrings(c.Cockpit.DisabledRules)
	out.Cockpit.Priority = cloneStrings(c.Cockpit.Priority)
	out.Cockpit.LongTailIgnoreProperties = cloneStrings(c.Cockpit.LongTailIgnoreProperties)
	out.Cockpit.ReviewStatuses = cloneStrings(c.Cockpit.ReviewStatuses)
	out.Cockpit.WIPStatuses = cloneStrings(c.Cockpit.WIPStatuses)
	out.Cockpit.HubTypeValues = cloneStrings(c.Cockpit.HubTypeValues)
	out.Cockpit.HubEmptyHeadings = cloneStrings(c.Cockpit.HubEmptyHeadings)
	out.Cockpit.SensitiveIgnorePaths = cloneStrings(c.Cockpit.SensitiveIgnorePaths)
	out.Cockpit.SensitiveIgnorePatterns = cloneStrings(c.Cockpit.SensitiveIgnorePatterns)
	if c.Serve.OpenBrowser != nil {
		enabled := *c.Serve.OpenBrowser
		out.Serve.OpenBrowser = &enabled
	}
	return &out
}

// Clone returns a deep copy of one property definition.
func (d PropertyDefinition) Clone() PropertyDefinition {
	d.Allowed = cloneStrings(d.Allowed)
	return d
}

func cloneProperties(in map[string]PropertyDefinition) map[string]PropertyDefinition {
	if in == nil {
		return nil
	}
	out := make(map[string]PropertyDefinition, len(in))
	for key, def := range in {
		out[key] = def.Clone()
	}
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string{}, in...)
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
