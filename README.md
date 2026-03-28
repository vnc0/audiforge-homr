# audiforge-homr

`audiforge-homr` is a Dockerized Go web service that mirrors the original Audiforge interface, but swaps out Audiveris for [`homr`](https://github.com/liebharc/homr) and [`relieur`](https://github.com/papoteur-mga/relieur).

## How it works

1. Upload a PDF, PNG, JPG, or JPEG file to `/upload`.
2. PDF files are rasterized into page images with `pdftoppm`.
3. Each page image is converted into MusicXML with `homr`.
4. Multi-page results are merged into a single `MusicXML` file with `relieur`.
5. The merged score is downloaded from `/download/{id}`.

## Endpoints

- `GET /`: web UI
- `POST /upload`: accepts `multipart/form-data` with a `file` field
- `GET /status/{id}`: returns job status JSON
- `GET /download/{id}`: downloads the final `MusicXML`

## Run with Docker

```bash
docker build -t audiforge-homr .
docker run --rm -p 8080:8080 audiforge-homr
```

Set `LOG=debug` on the container if you want `homr`, `relieur`, and page conversion logs echoed to stdout.

## Local development

Requirements:

- Go 1.24+
- Python 3.11
- `pdftoppm` from `poppler-utils`

Then install the Python tools and start the server:

```bash
pip install git+https://github.com/liebharc/homr.git
pip install git+https://github.com/papoteur-mga/relieur.git
homr --init
go run .
```
