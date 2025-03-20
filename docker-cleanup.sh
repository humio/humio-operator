#!/bin/bash

# Script to remove Docker images older than 30 days
# For macOS systems

echo "===== Docker Image Cleanup ====="
echo "Finding images older than 30 days..."

# Get current date in seconds since epoch
current_date=$(date +%s)
# 30 days in seconds (30 * 24 * 60 * 60)
thirty_days_in_seconds=$((30 * 24 * 60 * 60))

# Track metrics
images_checked=0
images_removed=0
space_freed=0

# Get all images with their creation dates
docker_images=$(docker images --format "{{.ID}}|{{.Repository}}|{{.Tag}}|{{.CreatedAt}}|{{.Size}}")

# Check if there are any images
if [ -z "$docker_images" ]; then
    echo "No Docker images found on this system."
    exit 0
fi

echo "Analyzing Docker images..."

# Process each image
while IFS= read -r line; do
    # Parse the image data
    image_id=$(echo "$line" | cut -d'|' -f1)
    repo=$(echo "$line" | cut -d'|' -f2)
    tag=$(echo "$line" | cut -d'|' -f3)
    created_at=$(echo "$line" | cut -d'|' -f4)
    size=$(echo "$line" | cut -d'|' -f5)
    
    # Convert the creation date to seconds since epoch (macOS date handling)
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # Format for macOS
        # Example: 2023-01-15 14:33:45 -0700 MST
        created_date=$(date -j -f "%Y-%m-%d %H:%M:%S %z %Z" "$(echo $created_at)" +%s 2>/dev/null)
        
        # If the above format fails, try alternative format
        if [ -z "$created_date" ]; then
            # Example: 2023-01-15T14:33:45
            created_date=$(date -j -f "%Y-%m-%dT%H:%M:%S" "$(echo $created_at | cut -d' ' -f1)" +%s 2>/dev/null)
        fi
    fi
    
    # If still no valid date, skip this image
    if [ -z "$created_date" ]; then
        echo "Warning: Could not parse date for image $repo:$tag ($image_id). Skipping."
        continue
    fi
    
    images_checked=$((images_checked + 1))
    
    # Calculate the age of the image in seconds
    image_age=$((current_date - created_date))
    
    # Check if the image is older than 30 days
    if [ $image_age -gt $thirty_days_in_seconds ]; then
        echo "Removing old image: $repo:$tag ($image_id) - Created: $created_at"
        
        # Get image size before removing (for reporting)
        image_size_bytes=$(docker image inspect "$image_id" --format='{{.Size}}' 2>/dev/null)
        
        # Remove the image
        docker rmi "$image_id" >/dev/null 2>&1
        
        # Check if removal was successful
        if [ $? -eq 0 ]; then
            images_removed=$((images_removed + 1))
            
            # Convert bytes to MB for reporting
            if [ ! -z "$image_size_bytes" ]; then
                size_mb=$((image_size_bytes / 1024 / 1024))
                space_freed=$((space_freed + size_mb))
            fi
        else
            echo "  Warning: Failed to remove image $repo:$tag. It may be in use."
        fi
    fi
done <<< "$docker_images"

# Report results
echo "===== Cleanup Summary ====="
echo "Images checked: $images_checked"
echo "Images removed: $images_removed"
echo "Approximate space freed: $space_freed MB"

if [ $images_removed -eq 0 ]; then
    echo "No images older than 30 days were found or removed."
else
    echo "Successfully removed $images_removed old Docker images."
fi

echo "Cleanup completed."

