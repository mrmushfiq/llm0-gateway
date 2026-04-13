"""
LLM0 Embedding Service
Provides text embeddings using sentence-transformers (all-MiniLM-L6-v2)
Phase 2 MVP: Simple, production-ready, 50-100 req/s per instance
"""
from fastapi import FastAPI, HTTPException
from sentence_transformers import SentenceTransformer
from pydantic import BaseModel, Field
from typing import List
import logging

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = FastAPI(
    title="LLM0 Embedding Service",
    description="Self-hosted embeddings for semantic caching",
    version="1.0.0"
)

# Load model on startup (keep in memory)
logger.info("Loading sentence-transformers model: all-MiniLM-L6-v2")
model = SentenceTransformer('sentence-transformers/all-MiniLM-L6-v2')
logger.info("Model loaded successfully")


class EmbedRequest(BaseModel):
    """Request body for embedding generation"""
    texts: List[str] = Field(..., description="List of texts to embed", min_items=1, max_items=100)


class EmbedResponse(BaseModel):
    """Response body containing embeddings"""
    embeddings: List[List[float]] = Field(..., description="List of 384-dimensional embeddings")
    model: str = Field(default="all-MiniLM-L6-v2", description="Model used for embeddings")
    dimensions: int = Field(default=384, description="Embedding dimensions")


@app.get("/")
async def root():
    """Root endpoint"""
    return {
        "service": "LLM0 Embedding Service",
        "model": "all-MiniLM-L6-v2",
        "dimensions": 384,
        "status": "healthy"
    }


@app.get("/health")
async def health():
    """Health check endpoint"""
    return {
        "status": "healthy",
        "model": "all-MiniLM-L6-v2",
        "dimensions": 384
    }


@app.post("/embed", response_model=EmbedResponse)
async def embed(req: EmbedRequest):
    """
    Generate embeddings for input texts.
    
    - **texts**: List of texts to embed (1-100 texts)
    - Returns 384-dimensional normalized embeddings
    """
    try:
        # Generate embeddings with normalization for better cosine similarity
        embeddings = model.encode(
            req.texts,
            normalize_embeddings=True,
            show_progress_bar=False
        )
        
        # Convert numpy arrays to lists for JSON serialization
        embeddings_list = embeddings.tolist()
        
        logger.info(f"Generated {len(embeddings_list)} embeddings")
        
        return EmbedResponse(
            embeddings=embeddings_list,
            model="all-MiniLM-L6-v2",
            dimensions=384
        )
    
    except Exception as e:
        logger.error(f"Embedding generation failed: {str(e)}")
        raise HTTPException(status_code=500, detail=f"Embedding generation failed: {str(e)}")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8080)

