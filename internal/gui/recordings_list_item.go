//go:build !headless

package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

var _ desktop.Mouseable = (*recordingsListItem)(nil)
var _ fyne.Tappable = (*recordingsListItem)(nil)

type recordingsListItem struct {
	widget.BaseWidget
	background  *canvas.Rectangle
	label       *widget.Label
	id          widget.ListItemID
	selected    bool
	onMouseDown func(fyne.KeyModifier)
	onTapped    func(widget.ListItemID)
}

func newRecordingsListItem(onMouseDown func(fyne.KeyModifier), onTapped func(widget.ListItemID)) *recordingsListItem {
	item := &recordingsListItem{
		label:       widget.NewLabel(""),
		onMouseDown: onMouseDown,
		onTapped:    onTapped,
	}
	item.ExtendBaseWidget(item)
	return item
}

func (i *recordingsListItem) SetID(id widget.ListItemID) {
	i.id = id
}

func (i *recordingsListItem) SetText(text string) {
	i.label.SetText(text)
}

func (i *recordingsListItem) SetSelected(selected bool) {
	if i.selected == selected {
		return
	}
	i.selected = selected
	i.Refresh()
}

func (i *recordingsListItem) MouseDown(ev *desktop.MouseEvent) {
	if i.onMouseDown != nil {
		i.onMouseDown(ev.Modifier)
	}
}

func (i *recordingsListItem) MouseUp(_ *desktop.MouseEvent) {}

func (i *recordingsListItem) Tapped(_ *fyne.PointEvent) {
	if i.onTapped != nil {
		i.onTapped(i.id)
	}
}

func (i *recordingsListItem) CreateRenderer() fyne.WidgetRenderer {
	i.ExtendBaseWidget(i)
	i.background = canvas.NewRectangle(theme.Color(theme.ColorNameSelection))
	i.background.Hide()
	return &recordingsListItemRenderer{item: i, objects: []fyne.CanvasObject{i.background, i.label}}
}

type recordingsListItemRenderer struct {
	item    *recordingsListItem
	objects []fyne.CanvasObject
}

func (r *recordingsListItemRenderer) Layout(size fyne.Size) {
	r.item.background.Resize(size)
	r.item.label.Resize(size)
}

func (r *recordingsListItemRenderer) MinSize() fyne.Size {
	return r.item.label.MinSize()
}

func (r *recordingsListItemRenderer) Refresh() {
	if r.item.selected {
		r.item.background.FillColor = r.item.Theme().Color(theme.ColorNameSelection, fyne.CurrentApp().Settings().ThemeVariant())
		r.item.background.Show()
	} else {
		r.item.background.Hide()
	}
	r.item.background.Refresh()
	r.item.label.Refresh()
}

func (r *recordingsListItemRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *recordingsListItemRenderer) Destroy() {}
