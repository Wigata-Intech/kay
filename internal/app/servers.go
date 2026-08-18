package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// Server form field order.
const (
	fldAlias = iota
	fldHost
	fldPort
	fldUser
	fldKey
	numServerFields
)

// serverForm builds the five-field form, optionally prefilled from srv.
func (c *Console) serverForm(srv *config.Server) tui.Form {
	f := tui.Form{Fields: []tui.Field{
		{Label: "Alias"}, {Label: "Host"}, {Label: "Port"}, {Label: "User"}, {Label: "Key"},
	}}
	if srv != nil {
		f.Fields[fldAlias].Input.SetValue(srv.Alias)
		f.Fields[fldHost].Input.SetValue(srv.Host)
		f.Fields[fldPort].Input.SetValue(strconv.Itoa(srv.Port))
		f.Fields[fldUser].Input.SetValue(srv.User)
		f.Fields[fldKey].Input.SetValue(srv.KeyName)
		return f
	}
	f.Fields[fldPort].Input.SetValue("22")
	if len(c.store.Keys) == 1 {
		f.Fields[fldKey].Input.SetValue(c.store.Keys[0].Name)
	}
	return f
}

// parseServer validates the form values, returning the server or per-field
// messages (nil when everything checks out).
func (c *Console) parseServer(vals []string) (*config.Server, []string) {
	errs := make([]string, numServerFields)
	srv := &config.Server{
		Alias:   strings.TrimSpace(vals[fldAlias]),
		Host:    strings.TrimSpace(vals[fldHost]),
		User:    strings.TrimSpace(vals[fldUser]),
		KeyName: strings.TrimSpace(vals[fldKey]),
	}
	bad := false
	for _, r := range []struct {
		idx int
		val string
	}{{fldAlias, srv.Alias}, {fldHost, srv.Host}, {fldUser, srv.User}} {
		if r.val == "" {
			errs[r.idx] = "required"
			bad = true
		}
	}
	port, err := strconv.Atoi(strings.TrimSpace(vals[fldPort]))
	if err != nil || port < 1 || port > 65535 {
		errs[fldPort] = "must be a port number (1-65535)"
		bad = true
	}
	srv.Port = port
	if srv.KeyName == "" {
		errs[fldKey] = "required — generate one in the keys view (K)"
		bad = true
	} else if _, err := c.store.FindKey(srv.KeyName); err != nil {
		errs[fldKey] = "no such key"
		bad = true
	}
	if bad {
		return nil, errs
	}
	return srv, nil
}

// addServerView is the `a` screen: register a server and rebuild the fleet.
func (c *Console) addServerView() View {
	return &formView{title: "Add server", form: c.serverForm(nil), submit: func(vals []string) ([]string, error) {
		srv, fieldErrs := c.parseServer(vals)
		if fieldErrs != nil {
			return fieldErrs, nil
		}
		if err := c.store.AddServer(*srv); err != nil {
			return nil, err
		}
		if err := c.store.Save(); err != nil {
			return nil, err
		}
		c.markHostsDirty()
		return nil, nil
	}}
}

// editServerView is the `e` screen: update the highlighted server in place.
func (c *Console) editServerView(orig config.Server) View {
	return &formView{title: "Edit server " + orig.Alias, form: c.serverForm(&orig), submit: func(vals []string) ([]string, error) {
		srv, fieldErrs := c.parseServer(vals)
		if fieldErrs != nil {
			return fieldErrs, nil
		}
		cur, err := c.store.FindServer(orig.Alias)
		if err != nil {
			return nil, err // deleted underneath the form
		}
		if srv.Alias != orig.Alias {
			if _, err := c.store.FindServer(srv.Alias); err == nil {
				errs := make([]string, numServerFields)
				errs[fldAlias] = "alias already in use"
				return errs, nil
			}
		}
		*cur = *srv
		if err := c.store.Save(); err != nil {
			return nil, err
		}
		c.markHostsDirty()
		return nil, nil
	}}
}

// deleteServerView is the `d` screen: a confirm modal that removes the
// highlighted server and rebuilds the fleet.
func (c *Console) deleteServerView(srv config.Server) View {
	return &confirmView{
		title: "Delete server",
		text:  []string{fmt.Sprintf("Remove %s (%s@%s:%d)?", srv.Alias, srv.User, srv.Host, srv.Port)},
		respond: func(ok bool) {
			if !ok {
				return
			}
			if err := c.removeServer(srv.Alias); err != nil {
				c.status = tui.Red(err.Error())
				return
			}
			c.markHostsDirty()
		},
	}
}

// removeServer drops the server from the store and persists the change.
func (c *Console) removeServer(alias string) error {
	if err := c.store.RemoveServer(alias); err != nil {
		return err
	}
	return c.store.Save()
}
