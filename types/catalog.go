// Copyright (c) 2026 BiaZap (fork patch #11 — catalog READ API)
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// READ ONLY — the write path (ProductInput + ProductCreate/Update/Delete) was
// removed 2026-05-24 after the product_catalog_add IQ proved to hang with no
// matched response. See catalog.go header + whatsmeow-fork.md patch #11.

package types

// ProductImage is a single image attached to a catalog product. RequestURL
// and OriginalURL point at WhatsApp's media CDN (mmg.whatsapp.net /
// *.fbcdn.net). The embedding application MUST NOT expose these URLs to its
// own consumers directly — cache them through the instance proxy and re-emit
// an application URL (see BiaZap proxy-discipline lesson #31/#35).
type ProductImage struct {
	// RequestURL is the request_image_url returned by the catalog read path.
	RequestURL string
	// OriginalURL is the original_image_url returned by the catalog read path.
	OriginalURL string
}

// Product mirrors a single product in a WhatsApp Business catalog.
type Product struct {
	ID          string
	Name        string
	Description string
	RetailerID  string
	URL         string
	Price       int64
	Currency    string
	IsHidden    bool
	// ReviewStatus is the WhatsApp review verdict ("APPROVED", "PENDING", etc).
	ReviewStatus string
	// Images carries the parsed media URLs for the product (read path).
	Images []ProductImage
}

// Catalog is the result of GetCatalog: a page of products plus the cursor
// to fetch the next page (empty when there are no more pages).
type Catalog struct {
	Products       []Product
	NextPageCursor string
}

// CatalogCollection is a named grouping of products in a business catalog.
type CatalogCollection struct {
	ID       string
	Name     string
	Status   string
	CanAppeal bool
	Products []Product
}

// Collections is the result of GetCollections.
type Collections struct {
	Collections []CatalogCollection
}
