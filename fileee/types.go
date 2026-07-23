package fileee

// Operator ist die vollständige Query-DSL-Operator-Enum
// (io.fileee.shared.storage.query.Operator, 21 Werte, API.md §6.2).
type Operator string

const (
	OpAfter        Operator = "AFTER"
	OpBefore       Operator = "BEFORE"
	OpBiggerEqual  Operator = "BIGGER_EQUAL"
	OpBigger       Operator = "BIGGER"
	OpEQ           Operator = "EQ"
	OpNEQ          Operator = "NEQ"
	OpLike         Operator = "LIKE"
	OpFuzzy        Operator = "FUZZY"
	OpSmallerEqual Operator = "SMALLER_EQUAL"
	OpSmaller      Operator = "SMALLER"
	OpHas          Operator = "HAS"
	OpHasAny       Operator = "HAS_ANY"
	OpHasNone      Operator = "HAS_NONE"
	OpNotIn        Operator = "NOT_IN"
	OpIn           Operator = "IN"
	OpHasAll       Operator = "HAS_ALL"
	OpExists       Operator = "EXISTS"
	OpDoesNotExist Operator = "DOES_NOT_EXIST"
	OpOr           Operator = "OR"
	OpAnd          Operator = "AND"
	OpHasElements  Operator = "HAS_ELEMENTS"
)

// PublicDocumentStatus ist der reale Verarbeitungsstatus eines Dokuments
// (io.fileee.shared.enums.PublicDocumentStatus, 11 Werte, API.md §6.1).
type PublicDocumentStatus string

const (
	StatusUploading          PublicDocumentStatus = "UPLOADING"
	StatusIP                 PublicDocumentStatus = "IP"
	StatusOCR                PublicDocumentStatus = "OCR"
	StatusAnalysing          PublicDocumentStatus = "ANALYSING"
	StatusClassified         PublicDocumentStatus = "CLASSIFIED"
	StatusDone               PublicDocumentStatus = "DONE"
	StatusDeleted            PublicDocumentStatus = "DELETED"
	StatusDeletedPermanently PublicDocumentStatus = "DELETED_PERMANENTLY"
	StatusError              PublicDocumentStatus = "ERROR"
	StatusLocal              PublicDocumentStatus = "LOCAL"
	StatusNew                PublicDocumentStatus = "NEW"
)

// PDFMode steuert GET /api/v1/documents/:id/pdf?mode=... (API.md §4.1, vollständig belegt).
type PDFMode string

const (
	PDFModeDownload PDFMode = "download"
	PDFModePrint    PDFMode = "print"
)

// ImageSize steuert GET /api/v1/pages/:id/image?size=... (API.md §4.1, vollständig belegt).
type ImageSize string

const (
	ImageSizeSmedium ImageSize = "smedium"
	ImageSizeMedium  ImageSize = "medium"
)

// ContactType (io.fileee.shared.domain.dtos.ContactType, API.md §6.4).
type ContactType string

const (
	ContactTypeMe      ContactType = "ME"
	ContactTypeCompany ContactType = "COMPANY"
	ContactTypePerson  ContactType = "PERSON"
)

// ContactStatus (io.fileee.shared.domain.dtos.ContactStatus, API.md §6.5).
type ContactStatus string

const (
	ContactStatusManaged ContactStatus = "MANAGED"
	ContactStatusLinked  ContactStatus = "LINKED"
	ContactStatusCustom  ContactStatus = "CUSTOM"
)

// DocumentAction sind UI-Aktionen auf Dokumenten, u.a. in Document.ForbiddenActions
// (io.fileee.shared.enums.DocumentAction, 15 Werte, API.md §6.3).
type DocumentAction string

const (
	ActionMerge        DocumentAction = "MERGE"
	ActionDelete       DocumentAction = "DELETE"
	ActionSplit        DocumentAction = "SPLIT"
	ActionDownload     DocumentAction = "DOWNLOAD"
	ActionShare        DocumentAction = "SHARE"
	ActionExport       DocumentAction = "EXPORT"
	ActionExtractPages DocumentAction = "EXTRACT_PAGES"
	ActionEdit         DocumentAction = "EDIT"
	ActionEditReminder DocumentAction = "EDIT_REMINDER"
	ActionEditTags     DocumentAction = "EDIT_TAGS"
	ActionRotatePages  DocumentAction = "ROTATE_PAGES"
	ActionDeletePages  DocumentAction = "DELETE_PAGES"
	ActionReorderPages DocumentAction = "REORDER_PAGES"
	ActionView         DocumentAction = "VIEW"
	ActionRevisionLock DocumentAction = "REVISION_LOCK"
)

// CrudOperation ist das generische CRUD-Enum, u.a. für SSE-Push-Events (API.md §6.6).
// In dieser Lib-Version nicht aktiv genutzt (Push/SSE ist nicht Scope von Sub-Projekt A),
// aber Teil des belegten Wire-Vokabulars und daher hier bewusst mitgeführt.
type CrudOperation string

const (
	CrudCreate CrudOperation = "CREATE"
	CrudRead   CrudOperation = "READ"
	CrudUpdate CrudOperation = "UPDATE"
	CrudDelete CrudOperation = "DELETE"
)
