package forum

import (
	"archive/zip"
	"fmt"

	"witmoot/internal/imvault"
)

func (a *App) exportImages(archive *zip.Writer, manifest *exportManifest, options exportOptions) error {
	for i := range manifest.Posts {
		for j := range manifest.Posts[i].Attachments {
			attachment := &manifest.Posts[i].Attachments[j]
			if err := options.ctx.Err(); err != nil {
				return err
			}
			attachment.Note = "Preview unavailable at export time. Open the Imvault link to find the original."
			data, mime, err := a.exportImage(options, *attachment)
			if err != nil {
				continue
			}
			ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif"}[mime]
			attachment.Path = fmt.Sprintf("images/%d.%s", attachment.ID, ext)
			entry, err := archive.Create(attachment.Path)
			if err != nil {
				return err
			}
			if _, err := entry.Write(data); err != nil {
				return err
			}
			attachment.Note = ""
		}
	}
	return nil
}

func (a *App) exportImage(options exportOptions, attachment exportAttachment) ([]byte, string, error) {
	if a.vault == nil || attachment.Server != a.vault.Base {
		return nil, "", imvault.ErrUnavailable
	}
	img, err := a.store.Attachment(options.ctx, attachment.ID, options.user)
	if err != nil {
		return nil, "", err
	}
	token := ""
	if img.CredentialUserID.Valid {
		token, err = a.imageToken(options.ctx, img.CredentialUserID.Int64)
		if err != nil {
			return nil, "", err
		}
		if _, err := a.vault.File(options.ctx, token, img.RemoteID); err != nil {
			return nil, "", err
		}
	}
	return a.vault.Image(options.ctx, token, img.RemoteID, img.Rendition)
}
