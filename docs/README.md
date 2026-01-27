# GPU Provider Documentation

This directory contains the Hugo-based documentation site for GPU Provider, using the Docsy theme to match the Kubernetes documentation style.

## Prerequisites

- [Hugo Extended](https://gohugo.io/installation/) version 0.110.0 or later
- [Go](https://go.dev/doc/install) version 1.20 or later (required for Docsy theme)
- [Git](https://git-scm.com/) for managing theme submodules

## Quick Start

### 1. Install Hugo Extended

On Ubuntu/Debian:
```bash
wget https://github.com/gohugoio/hugo/releases/download/v0.121.0/hugo_extended_0.121.0_linux-amd64.deb
sudo dpkg -i hugo_extended_0.121.0_linux-amd64.deb
```

On macOS:
```bash
brew install hugo
```

### 2. Install Go (if not already installed)

On Ubuntu/Debian:
```bash
sudo apt install golang-go
```

On macOS:
```bash
brew install go
```

### 3. Initialize Theme Dependencies

From the docs directory:
```bash
cd themes/docsy
npm install
cd ../..
```

## Useful Commands

### Development Server

Start the Hugo development server with live reload:
```bash
cd docs
hugo server
```

The site will be available at http://localhost:1313/

For development with drafts included:
```bash
hugo server --buildDrafts
```

To bind to all interfaces (useful for remote access):
```bash
hugo server --bind 0.0.0.0
```

### Building the Site

Build the static site for production:
```bash
cd docs
hugo
```

The generated site will be in the `public/` directory.

Build with minification:
```bash
hugo --minify
```

### Content Management

Create a new documentation page:
```bash
hugo new docs/section-name/page-name.md
```

Create a new blog post:
```bash
hugo new blog/post-title.md
```

### Cleaning

Remove generated files:
```bash
rm -rf public/ resources/_gen/
```

### Checking for Issues

Check for broken links and other issues:
```bash
hugo --gc --minify --cleanDestinationDir
```

## Project Structure

```
docs/
├── archetypes/          # Content templates
├── content/             # Documentation content
│   └── en/             # English content
│       ├── _index.md   # Home page
│       └── docs/       # Documentation pages
├── static/             # Static assets (images, etc.)
├── themes/             # Hugo themes
│   └── docsy/         # Docsy theme (git submodule)
├── hugo.toml          # Hugo configuration
└── README.md          # This file
```

## Updating the Docsy Theme

To update the Docsy theme to the latest version:
```bash
cd docs/themes/docsy
git pull origin main
git submodule update --init --recursive
cd ../..
```

## Deployment

### Manual Deployment

1. Build the site:
   ```bash
   hugo --minify
   ```

2. Deploy the `public/` directory to your web server

### Automated Deployment (GitLab CI)

Add this to `.gitlab-ci.yml` to automatically build and deploy:

```yaml
pages:
  stage: deploy
  image: klakegg/hugo:0.121.0-ext-alpine
  script:
    - cd docs
    - hugo --minify
    - mv public ../public
  artifacts:
    paths:
      - public
  only:
    - main
```

### Static Site Hosting Options

- **GitLab Pages**: Automatically deploys from CI/CD
- **GitHub Pages**: Use GitHub Actions
- **Netlify**: Connect your repository
- **Vercel**: Connect your repository
- **AWS S3 + CloudFront**: Upload public/ folder

## Customization

### Configuration

Edit `hugo.toml` to customize:
- Site title and description
- Repository URLs
- UI settings
- Menu items

### Styling

To customize the theme:
1. Create `assets/scss/_variables_project.scss`
2. Override Docsy variables

### Adding Content

Follow the [Docsy documentation](https://www.docsy.dev/docs/adding-content/) for content guidelines.

## Troubleshooting

### Hugo Server Not Starting

- Ensure Hugo Extended is installed (not standard Hugo)
- Check Go is installed and in PATH
- Verify theme submodule is initialized

### Theme Not Loading

```bash
git submodule update --init --recursive
cd themes/docsy
npm install
```

### Build Errors

Check Hugo version:
```bash
hugo version
```

Ensure you have Extended version 0.110.0 or later.

## Resources

- [Hugo Documentation](https://gohugo.io/documentation/)
- [Docsy Theme Documentation](https://www.docsy.dev/docs/)
- [Kubernetes Documentation Style Guide](https://kubernetes.io/docs/contribute/style/style-guide/)
- [Markdown Guide](https://www.markdownguide.org/)
