"""
Streaming anomaly detection using Welford's algorithm + MAD
Production-optimized for real-time detection with adaptive thresholds
"""
import numpy as np
from typing import Tuple, List
from collections import deque
import threading


class WelfordMADDetector:
    """
    Online anomaly detection using:
    1. Welford's algorithm for numerically stable mean/variance
    2. MAD (Median Absolute Deviation) for robust outlier detection
    3. Adaptive thresholds based on recent history
    
    Thread-safe for concurrent updates from multiple streams
    """
    
    def __init__(self, window_size: int = 1000, mad_threshold: float = 3.5):
        self.window_size = window_size
        self.mad_threshold = mad_threshold
        
        # Welford's algorithm state
        self.count = 0
        self.mean = 0.0
        self.m2 = 0.0  # sum of squared differences from mean
        
        # MAD calculation
        self.history = deque(maxlen=window_size)
        
        # Adaptive threshold
        self.threshold_history = deque(maxlen=100)
        self.current_threshold = mad_threshold
        
        # Thread safety
        self.lock = threading.RLock()
    
    def update(self, value: float) -> Tuple[float, bool]:
        """
        Update statistics with new value using Welford's algorithm
        Returns: (anomaly_score, is_anomaly)
        
        Welford's algorithm advantages:
        - Numerically stable (no catastrophic cancellation)
        - Single pass (no need to store all values)
        - O(1) memory and time per update
        """
        with self.lock:
            self.count += 1
            delta = value - self.mean
            self.mean += delta / self.count
            delta2 = value - self.mean
            self.m2 += delta * delta2
            
            # Add to history for MAD calculation
            self.history.append(value)
            
            # Calculate anomaly score
            score, is_anomaly = self._calculate_anomaly(value)
            
            # Update adaptive threshold
            if not is_anomaly:
                self.threshold_history.append(score)
                self._update_threshold()
            
            return score, is_anomaly
    
    def batch_update(self, values: np.ndarray) -> Tuple[np.ndarray, np.ndarray]:
        """
        Batch update for efficiency
        Returns: (scores, is_anomaly_array)
        """
        scores = np.zeros(len(values))
        is_anomaly = np.zeros(len(values), dtype=bool)
        
        for i, value in enumerate(values):
            scores[i], is_anomaly[i] = self.update(float(value))
        
        return scores, is_anomaly
    
    def _calculate_anomaly(self, value: float) -> Tuple[float, bool]:
        """
        Calculate anomaly score using MAD (Median Absolute Deviation)
        
        MAD advantages over standard deviation:
        - Robust to outliers (doesn't use mean)
        - Scale-invariant
        - Better for skewed distributions
        """
        if len(self.history) < 10:
            return 0.0, False
        
        # Calculate median
        median = float(np.median(list(self.history)))
        
        # Calculate MAD
        abs_deviations = np.abs(np.array(list(self.history)) - median)
        mad = float(np.median(abs_deviations))
        
        if mad == 0:
            mad = 1e-8  # avoid division by zero
        
        # Modified Z-score using MAD
        # z_score = 0.6745 * (value - median) / mad
        # 0.6745 is the 0.75 quantile of standard normal (makes MAD comparable to std)
        z_score = 0.6745 * abs(value - median) / mad
        
        # Normalize to 0-1 range for consistency
        anomaly_score = min(1.0, z_score / 10.0)
        
        # Check against adaptive threshold
        is_anomaly = z_score > self.current_threshold
        
        return anomaly_score, is_anomaly
    
    def _update_threshold(self):
        """
        Adapt threshold based on recent normal behavior
        Increases threshold if seeing many near-misses
        Decreases if everything is clearly normal
        """
        if len(self.threshold_history) < 20:
            return
        
        recent_scores = list(self.threshold_history)
        p95 = np.percentile(recent_scores, 95)
        
        # Adaptive rule: if P95 is high, increase threshold (less sensitive)
        if p95 > 0.7:
            self.current_threshold = min(5.0, self.current_threshold * 1.05)
        elif p95 < 0.3:
            self.current_threshold = max(2.0, self.current_threshold * 0.95)
    
    @property
    def variance(self) -> float:
        """Current variance (population variance)"""
        with self.lock:
            if self.count < 2:
                return 0.0
            return self.m2 / self.count
    
    @property
    def stddev(self) -> float:
        """Current standard deviation"""
        return np.sqrt(self.variance)
    
    def get_stats(self) -> dict:
        """Get current statistics"""
        with self.lock:
            return {
                "count": self.count,
                "mean": self.mean,
                "stddev": self.stddev,
                "current_threshold": self.current_threshold,
                "history_size": len(self.history),
            }
    
    def reset(self):
        """Reset all statistics"""
        with self.lock:
            self.count = 0
            self.mean = 0.0
            self.m2 = 0.0
            self.history.clear()
            self.threshold_history.clear()
            self.current_threshold = self.mad_threshold


class MultiVariateWelfordDetector:
    """
    Multi-variate anomaly detection
    Each feature has independent Welford detector
    Aggregate anomaly score using weighted sum
    """
    
    def __init__(self, n_features: int, window_size: int = 1000):
        self.n_features = n_features
        self.detectors = [
            WelfordMADDetector(window_size=window_size)
            for _ in range(n_features)
        ]
        self.feature_weights = np.ones(n_features) / n_features
    
    def update(self, features: np.ndarray) -> Tuple[float, bool]:
        """
        Update with multi-dimensional feature vector
        Returns: (aggregate_score, is_anomaly)
        """
        if len(features) != self.n_features:
            raise ValueError(f"Expected {self.n_features} features, got {len(features)}")
        
        scores = []
        anomaly_flags = []
        
        for i, value in enumerate(features):
            score, is_anom = self.detectors[i].update(float(value))
            scores.append(score)
            anomaly_flags.append(is_anom)
        
        # Weighted aggregate score
        aggregate_score = float(np.dot(scores, self.feature_weights))
        
        # Anomaly if any feature or aggregate score is high
        is_anomaly = any(anomaly_flags) or aggregate_score > 0.8
        
        return aggregate_score, is_anomaly
    
    def set_feature_weights(self, weights: np.ndarray):
        """Set custom feature importance weights"""
        if len(weights) != self.n_features:
            raise ValueError(f"Expected {self.n_features} weights")
        
        # Normalize
        self.feature_weights = weights / np.sum(weights)
    
    def get_stats(self) -> dict:
        """Get stats for all features"""
        return {
            f"feature_{i}": detector.get_stats()
            for i, detector in enumerate(self.detectors)
        }
