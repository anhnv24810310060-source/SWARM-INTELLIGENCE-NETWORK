"""
Autoencoder-based anomaly detection training pipeline
Exports to ONNX for production inference
"""
import torch
import torch.nn as nn
import torch.optim as optim
from torch.utils.data import DataLoader, TensorDataset
import numpy as np
from pathlib import Path
import json
from typing import List, Tuple
import argparse


class Autoencoder(nn.Module):
    """
    Deep Autoencoder for anomaly detection
    Architecture: [input_dim -> 128 -> 64 -> 32 -> 64 -> 128 -> input_dim]
    """
    
    def __init__(self, input_dim: int, latent_dim: int = 32):
        super(Autoencoder, self).__init__()
        
        # Encoder
        self.encoder = nn.Sequential(
            nn.Linear(input_dim, 128),
            nn.ReLU(),
            nn.BatchNorm1d(128),
            nn.Dropout(0.2),
            nn.Linear(128, 64),
            nn.ReLU(),
            nn.BatchNorm1d(64),
            nn.Linear(64, latent_dim),
        )
        
        # Decoder
        self.decoder = nn.Sequential(
            nn.Linear(latent_dim, 64),
            nn.ReLU(),
            nn.BatchNorm1d(64),
            nn.Linear(64, 128),
            nn.ReLU(),
            nn.BatchNorm1d(128),
            nn.Dropout(0.2),
            nn.Linear(128, input_dim),
        )
    
    def forward(self, x):
        encoded = self.encoder(x)
        decoded = self.decoder(encoded)
        return decoded


def train_autoencoder(
    X_train: np.ndarray,
    feature_names: List[str],
    epochs: int = 50,
    batch_size: int = 256,
    learning_rate: float = 0.001,
    latent_dim: int = 32,
    output_dir: str = "./models"
) -> Tuple[nn.Module, Dict]:
    """
    Train autoencoder and export to ONNX
    Returns: (model, metadata)
    """
    
    output_path = Path(output_dir)
    output_path.mkdir(parents=True, exist_ok=True)
    
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    print(f"Training on {device}")
    
    # Normalize data (0-1 scaling)
    X_min = X_train.min(axis=0)
    X_max = X_train.max(axis=0)
    X_range = X_max - X_min
    X_range[X_range == 0] = 1.0  # avoid division by zero
    X_normalized = (X_train - X_min) / X_range
    
    # Create dataset
    dataset = TensorDataset(torch.FloatTensor(X_normalized))
    dataloader = DataLoader(dataset, batch_size=batch_size, shuffle=True)
    
    # Initialize model
    input_dim = X_train.shape[1]
    model = Autoencoder(input_dim, latent_dim).to(device)
    
    # Loss and optimizer
    criterion = nn.MSELoss()
    optimizer = optim.Adam(model.parameters(), lr=learning_rate)
    scheduler = optim.lr_scheduler.ReduceLROnPlateau(optimizer, mode='min', factor=0.5, patience=5)
    
    # Training loop
    best_loss = float('inf')
    for epoch in range(epochs):
        model.train()
        epoch_loss = 0.0
        
        for batch_idx, (data,) in enumerate(dataloader):
            data = data.to(device)
            
            # Forward pass
            optimizer.zero_grad()
            reconstructed = model(data)
            loss = criterion(reconstructed, data)
            
            # Backward pass
            loss.backward()
            optimizer.step()
            
            epoch_loss += loss.item()
        
        avg_loss = epoch_loss / len(dataloader)
        scheduler.step(avg_loss)
        
        if avg_loss < best_loss:
            best_loss = avg_loss
        
        if (epoch + 1) % 10 == 0:
            print(f"Epoch [{epoch+1}/{epochs}], Loss: {avg_loss:.6f}")
    
    # Calculate threshold (95th percentile of reconstruction error on training data)
    model.eval()
    with torch.no_grad():
        X_tensor = torch.FloatTensor(X_normalized).to(device)
        reconstructed = model(X_tensor).cpu().numpy()
        errors = np.mean((X_normalized - reconstructed) ** 2, axis=1)
        threshold = float(np.percentile(errors, 95))
    
    # Export to ONNX
    dummy_input = torch.randn(1, input_dim).to(device)
    onnx_path = output_path / "autoencoder.onnx"
    
    torch.onnx.export(
        model,
        dummy_input,
        str(onnx_path),
        export_params=True,
        opset_version=14,
        do_constant_folding=True,
        input_names=['input'],
        output_names=['output'],
        dynamic_axes={'input': {0: 'batch_size'}, 'output': {0: 'batch_size'}}
    )
    
    # Save metadata
    metadata = {
        "version": f"v{int(time.time())}",
        "model_type": "autoencoder",
        "features": feature_names,
        "input_dim": input_dim,
        "latent_dim": latent_dim,
        "threshold": threshold,
        "best_loss": best_loss,
        "trained_samples": len(X_train),
        "normalization": {
            "min": X_min.tolist(),
            "max": X_max.tolist()
        }
    }
    
    metadata_path = output_path / "autoencoder.json"
    metadata_path.write_text(json.dumps(metadata, indent=2))
    
    print(f"\n✓ Model exported to {onnx_path}")
    print(f"✓ Metadata saved to {metadata_path}")
    print(f"✓ Threshold (95th percentile): {threshold:.6f}")
    
    return model, metadata


def generate_synthetic_training_data(n_samples: int = 10000, n_features: int = 50) -> Tuple[np.ndarray, List[str]]:
    """Generate synthetic network traffic data for demo"""
    
    # Normal traffic patterns
    normal = np.random.randn(int(n_samples * 0.95), n_features) * 0.5 + 1.0
    
    # Anomalies (5%)
    anomalies = np.random.randn(int(n_samples * 0.05), n_features) * 3.0 + 5.0
    
    X = np.vstack([normal, anomalies])
    np.random.shuffle(X)
    
    # Feature names
    feature_names = [
        f"feature_{i}" for i in range(n_features)
    ]
    
    # Make some features more realistic
    if n_features >= 10:
        feature_names[0] = "bytes_in"
        feature_names[1] = "bytes_out"
        feature_names[2] = "packets_in"
        feature_names[3] = "packets_out"
        feature_names[4] = "duration_ms"
        feature_names[5] = "connection_rate"
        feature_names[6] = "error_rate"
        feature_names[7] = "latency_p95"
        feature_names[8] = "retransmit_rate"
        feature_names[9] = "syn_flood_score"
    
    return X, feature_names


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Train Autoencoder for Anomaly Detection")
    parser.add_argument("--samples", type=int, default=10000, help="Number of training samples")
    parser.add_argument("--features", type=int, default=50, help="Number of features")
    parser.add_argument("--epochs", type=int, default=50, help="Training epochs")
    parser.add_argument("--latent-dim", type=int, default=32, help="Latent dimension")
    parser.add_argument("--output", type=str, default="./models", help="Output directory")
    
    args = parser.parse_args()
    
    print("Generating synthetic training data...")
    X_train, feature_names = generate_synthetic_training_data(args.samples, args.features)
    
    print(f"Training autoencoder on {len(X_train)} samples, {len(feature_names)} features...")
    train_autoencoder(
        X_train,
        feature_names,
        epochs=args.epochs,
        latent_dim=args.latent_dim,
        output_dir=args.output
    )
    
    print("\n✅ Training complete! Model ready for production inference.")
