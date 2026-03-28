document.addEventListener('DOMContentLoaded', () => {
  const dropZone = document.getElementById('drop-zone');
  const fileInput = document.getElementById('file-input');
  const processingDiv = document.getElementById('processing');
  const completeDiv = document.getElementById('complete');
  const subtitle = completeDiv.querySelector('.subtitle');
  const statusText = document.querySelector('.status-text');
  let currentConversionId = null;

  const allowedExtensions = ['.pdf', '.png', '.jpg', '.jpeg'];

  const handleDrag = (event) => {
    event.preventDefault();
    dropZone.style.borderColor = 'var(--brand-purple)';
    dropZone.style.background = 'rgba(255, 255, 255, 0.8)';
  };

  dropZone.addEventListener('dragover', handleDrag);
  dropZone.addEventListener('dragleave', resetDropZone);

  dropZone.addEventListener('drop', (event) => {
    event.preventDefault();
    handleFile(event.dataTransfer.files[0]);
    resetDropZone();
  });

  dropZone.addEventListener('click', () => fileInput.click());

  fileInput.addEventListener('change', (event) => {
    if (event.target.files.length > 0) {
      handleFile(event.target.files[0]);
      event.target.value = '';
    }
  });

  document.getElementById('download-again').addEventListener('click', () => {
    if (currentConversionId) {
      window.location.href = `/download/${currentConversionId}`;
    }
  });

  document.getElementById('new-conversion').addEventListener('click', resetUI);

  async function handleFile(file) {
    if (!file || !isSupportedFile(file.name)) {
      showError('Please choose a PDF, PNG, JPG, or JPEG file.');
      return;
    }

    const formData = new FormData();
    formData.append('file', file);

    try {
      showProcessing();
      const response = await fetch('/upload', {
        method: 'POST',
        body: formData
      });

      if (!response.ok) {
        throw new Error(await response.text());
      }

      const { id } = await response.json();
      currentConversionId = id;
      pollStatus(id);
    } catch (error) {
      showError(error.message);
    }
  }

  async function pollStatus(id) {
    try {
      const response = await fetch(`/status/${id}`);
      if (!response.ok) {
        throw new Error('Status check failed');
      }

      const status = await response.json();
      if (status.message) {
        statusText.textContent = status.message;
      }

      switch (status.status) {
        case 'completed':
          showConversionComplete(status.pageCount || 1);
          break;
        case 'error':
          throw new Error(status.message || 'Conversion failed');
        default:
          setTimeout(() => pollStatus(id), 1000);
      }
    } catch (error) {
      showError(error.message);
    }
  }

  function isSupportedFile(filename) {
    const lower = filename.toLowerCase();
    return allowedExtensions.some((extension) => lower.endsWith(extension));
  }

  function showProcessing() {
    dropZone.classList.add('hidden');
    processingDiv.classList.remove('hidden');
    completeDiv.classList.add('hidden');
    statusText.textContent = 'Analyzing your score...';
  }

  function showConversionComplete(pageCount) {
    subtitle.textContent = `Successfully converted ${pageCount} ${pageCount === 1 ? 'page' : 'pages'} into MusicXML`;
    processingDiv.classList.add('hidden');
    completeDiv.classList.remove('hidden');
  }

  function showError(message) {
    processingDiv.classList.add('hidden');
    completeDiv.innerHTML = `
      <h2 class="thank-you">Conversion Error</h2>
      <p class="subtitle">${message}</p>
      <div class="button-group">
        <button class="btn reset-btn" onclick="location.reload()">
          Try Again
        </button>
      </div>
    `;
    completeDiv.classList.remove('hidden');
  }

  function resetDropZone() {
    dropZone.style.borderColor = 'var(--brand-mid)';
    dropZone.style.background = 'rgba(255, 255, 255, 0.95)';
  }

  function resetUI() {
    currentConversionId = null;
    completeDiv.classList.add('hidden');
    dropZone.classList.remove('hidden');
    processingDiv.classList.add('hidden');
    resetDropZone();
  }
});
