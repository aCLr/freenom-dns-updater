package freenom

import (
	"context"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/libdns/libdns"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var client http.Client

func init() {
	cj, err := cookiejar.New(&cookiejar.Options{})
	if err != nil {
		panic(err)
	}

	client = http.Client{
		Transport:     nil,
		CheckRedirect: nil,
		Jar:           cj,
		Timeout:       0,
	}
}
func (p *Provider) login(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://my.freenom.com/", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	document, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return err
	}

	formNode := document.Find("form.form-stacked").Get(0)
	if formNode == nil {
		return errors.New("form not found")
	}

	tokenNode := formNode.FirstChild.NextSibling
	if !(tokenNode.Data == "input" && tokenNode.Attr[1].Key == "name" && tokenNode.Attr[1].Val == "token") {
		return errors.New("token not found")
	}

	form := url.Values{}
	form.Set("token", tokenNode.Attr[2].Val)
	form.Set("username", p.Email)
	form.Set("password", p.Password)
	form.Set("rememberme", "on")

	req, err = http.NewRequestWithContext(ctx, "POST", "https://my.freenom.com/dologin.php", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	req.Header.Add("Referer", "https://my.freenom.com/clientarea.php")

	resp, err = client.Do(req)
	if err != nil {
		return err
	}

	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), "Login Details Incorrect") {
		return errors.New("invalid login details")
	}

	return nil
}

func (p *Provider) getRecords(ctx context.Context, searchDomain string) ([]libdns.Record, error) {

	recordsDoc, err := p.getRecordsSelection(ctx, searchDomain)
	if err != nil {
		return nil, err
	}

	names := recordsDoc.Find("#recordslistform > table:nth-child(3) > tbody:nth-child(2) > tr > .name_column input").Nodes
	types := recordsDoc.Find("#recordslistform > table:nth-child(3) > tbody:nth-child(2) > tr > .type_column td strong").Nodes
	ttls := recordsDoc.Find("#recordslistform > table:nth-child(3) > tbody:nth-child(2) > tr > .ttl_column input").Nodes
	values := recordsDoc.Find("#recordslistform > table:nth-child(3) > tbody:nth-child(2) > tr > .value_column input").Nodes

	records := make([]libdns.Record, len(names))
	for i := 0; i < len(names); i++ {
		name := names[i].Attr[2].Val
		typ := types[i].Data
		ttl, err := strconv.Atoi(ttls[i].Attr[2].Val)
		if err != nil {
			return nil, err
		}

		value := values[i].Attr[2].Val
		records[i] = libdns.Record{
			Type:  typ,
			Name:  name,
			Value: value,
			TTL:   time.Second * time.Duration(ttl),
		}
	}

	return records, nil
}

func (p *Provider) getRecordsSelection(ctx context.Context, searchDomain string) (*goquery.Document, error) {
	// FIXME: login only if need
	if err := p.login(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://my.freenom.com/clientarea.php?action=domains", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	defer resp.Body.Close()
	if err != nil {
		return nil, err
	}

	domainsDoc, err := goquery.NewDocumentFromReader(resp.Body)

	domains := domainsDoc.Find("table .second > a").Nodes
	if len(domains) == 0 {
		return nil, errors.New("table rows not found")
	}

	var manageLink string

	for i, dom := range domains {
		if strings.TrimSpace(dom.FirstChild.Data) == searchDomain {
			manage := domainsDoc.Find(fmt.Sprintf(".table > tbody:nth-child(2) > tr:nth-child(%d) > td.seventh div a", i+1)).Get(0)
			manageLink = manage.Attr[1].Val
			break
		}
	}

	if manageLink == "" {
		return nil, errors.New("manage link not found")
	}

	vals, err := url.ParseQuery(manageLink)
	if err != nil {
		return nil, err
	}

	req, err = http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://my.freenom.com/clientarea.php?managedns=%s&domainid=%s", searchDomain, vals.Get("id")), nil)
	if err != nil {
		return nil, err
	}

	resp, err = client.Do(req)
	if err != nil {
		return nil, err
	}

	recordsDoc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}
	return recordsDoc, nil
}
