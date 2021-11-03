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

	recordsDoc, _, err := p.getRecordsSelection(ctx, searchDomain)
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
			ID:    strconv.Itoa(i),
			TTL:   time.Second * time.Duration(ttl),
		}
	}

	return records, nil
}

// TODO
func (p *Provider) loginIfNeed(ctx context.Context) error {
	return p.login(ctx)
}

func (p *Provider) getRecordsSelection(ctx context.Context, searchDomain string) (*goquery.Document, string, error) {
	if err := p.loginIfNeed(ctx); err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://my.freenom.com/clientarea.php?action=domains", nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := client.Do(req)
	defer resp.Body.Close()
	if err != nil {
		return nil, "", err
	}

	domainsDoc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, "", err
	}

	domains := domainsDoc.Find("table .second > a").Nodes
	if len(domains) == 0 {
		return nil, "", errors.New("table rows not found")
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
		return nil, "", errors.New("manage link not found")
	}

	vals, err := url.ParseQuery(manageLink)
	if err != nil {
		return nil, "", err
	}

	req, err = http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://my.freenom.com/clientarea.php?managedns=%s&domainid=%s", searchDomain, vals.Get("id")), nil)
	if err != nil {
		return nil, "", err
	}

	resp, err = client.Do(req)
	if err != nil {
		return nil, "", err
	}

	recordsDoc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return recordsDoc, vals.Get("id"), nil
}

func (p *Provider) appendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	recordsDoc, domID, err := p.getRecordsSelection(ctx, zone)
	if err != nil {
		return nil, err
	}

	formDock := recordsDoc.Find("#form ").Get(0)

	form := url.Values{}
	form.Set(formDock.FirstChild.Attr[1].Val, formDock.FirstChild.Attr[2].Val)
	form.Set(formDock.FirstChild.NextSibling.Attr[1].Val, formDock.FirstChild.NextSibling.Attr[2].Val)

	for i, rec := range records {
		form.Set(fmt.Sprintf("addrecord[%d][name]", i), rec.Name)
		form.Set(fmt.Sprintf("addrecord[%d][type]", i), rec.Type)
		form.Set(fmt.Sprintf("addrecord[%d][ttl]", i), strconv.Itoa(int(rec.TTL.Seconds())))
		form.Set(fmt.Sprintf("addrecord[%d][value]", i), rec.Value)
		form.Set(fmt.Sprintf("addrecord[%d][priority]", i), "")
		if rec.Priority > 0 {
			form.Set(fmt.Sprintf("addrecord[%d][priority]", i), strconv.Itoa(rec.Priority))
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("https://my.freenom.com/clientarea.php?managedns=%s&domainid=%s", zone, domID),
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	return p.getExistRecords(ctx, zone, records)
}

func (p *Provider) getExistRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {

	added := make([]libdns.Record, 0, len(records))
	newRecs, err := p.getRecords(ctx, zone)
	if err != nil {
		return nil, err
	}

	for _, rec := range newRecs {
		for _, expRec := range records {
			if rec == expRec {
				added = append(added, rec)
				break
			}
		}
	}

	return added, nil
}

func (p *Provider) setRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	recordsDoc, domID, err := p.getRecordsSelection(ctx, zone)
	if err != nil {
		return nil, err
	}

	formDock := recordsDoc.Find("#recordslistform ").Get(0)

	form := url.Values{}
	form.Set(formDock.FirstChild.Attr[1].Val, formDock.FirstChild.Attr[2].Val)
	form.Set(formDock.FirstChild.NextSibling.Attr[1].Val, formDock.FirstChild.NextSibling.Attr[2].Val)

	currentRecords, err := p.getRecords(ctx, zone)
	toAppend := make([]libdns.Record, 0, len(records))
	unused := make(map[libdns.Record]struct{}, len(currentRecords))
	for _, rec := range currentRecords {
		unused[rec] = struct{}{}
	}

	for _, expRec := range records {
		found := false

		for _, rec := range currentRecords {
			if rec.Type == expRec.Type && rec.Name == expRec.Name && (rec.TTL != expRec.TTL || rec.Value != expRec.Value || rec.Priority != expRec.Priority) {
				form.Set(fmt.Sprintf("records[%s][name]", rec.ID), expRec.Name)
				form.Set(fmt.Sprintf("records[%s][type]", rec.ID), expRec.Type)
				form.Set(fmt.Sprintf("records[%s][ttl]", rec.ID), strconv.Itoa(int(expRec.TTL.Seconds())))
				form.Set(fmt.Sprintf("records[%s][value]", rec.ID), expRec.Value)
				form.Set(fmt.Sprintf("records[%s][priority]", rec.ID), "")
				if rec.Priority > 0 {
					form.Set(fmt.Sprintf("addrecord[%s][priority]", rec.ID), strconv.Itoa(expRec.Priority))
				}
				found = true
				delete(unused, rec)
				break
			}
		}
		if !found {
			toAppend = append(toAppend, expRec)
		}
	}

	for rec := range unused {
		form.Set(fmt.Sprintf("records[%s][name]", rec.ID), rec.Name)
		form.Set(fmt.Sprintf("records[%s][type]", rec.ID), rec.Type)
		form.Set(fmt.Sprintf("records[%s][ttl]", rec.ID), strconv.Itoa(int(rec.TTL.Seconds())))
		form.Set(fmt.Sprintf("records[%s][value]", rec.ID), rec.Value)
		form.Set(fmt.Sprintf("records[%s][priority]", rec.ID), "")
		if rec.Priority > 0 {
			form.Set(fmt.Sprintf("addrecord[%s][priority]", rec.ID), strconv.Itoa(rec.Priority))
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("https://my.freenom.com/clientarea.php?managedns=%s&domainid=%s", zone, domID),
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	return p.getExistRecords(ctx, zone, records)
}

func (p *Provider) deleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	recs, err := p.getExistRecords(ctx, zone, records)

	recordsDoc, _, err := p.getRecordsSelection(ctx, zone)
	if err != nil {
		return nil, err
	}

	for i := range recs {
		recDoc := recordsDoc.Find(
			"#recordslistform > table:nth-child(3) > tbody:nth-child(2) > tr > .delete_column button",
		).Get(i)
		// if(confirm('Do you really want to remove this entry?')) location.href='/clientarea.php?managedns=domain.com&page=&records=A&dnsaction=delete&name=NC&value=199.247.29.197&line=&ttl=3600&priority=&weight=&port=&domainid=999999999
		jsCode := recDoc.Attr[3].Val
		href := strings.TrimLeft(jsCode, "if(confirm('Do you really want to remove this entry?')) location.href='")
		resp, err := client.Get("https://my.freenom.com" + href)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
	}

	deleted := make([]libdns.Record, 0, len(records))

	existRecs, err := p.getRecords(ctx, zone)
	if err != nil {
		return nil, err
	}

	for _, recToDelete := range records {
		found := false
		for _, rec := range existRecs {
			if rec == recToDelete {
				break
				found = true
			}
		}
		if !found {
			deleted = append(deleted, recToDelete)
		}
	}
	return deleted, nil

}
